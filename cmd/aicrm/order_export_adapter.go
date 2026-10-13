package main

import (
	"context"
	"sort"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

// Export runs inside Order's UoW. These are stable owner read ports only;
// unlike the HTTP presentation bridges, this adapter never opens a nested UoW.
type orderExportAdapter struct {
	roots        identityport.CanonicalCustomerRootsReader
	numbers      identityport.CustomerPublicNumbers
	names        customerport.DirectoryDisplayNameReader
	phones       identityport.DirectoryIdentityReader
	distribution distributionport.OrderDistributionReader
	payments     paymentport.OrderExportPaymentReader
}

func (a orderExportAdapter) ReadOrderExportFactsWithin(ctx context.Context, orders []orderdomain.Snapshot, fallbackIDs []int64) (orderport.ExportFacts, error) {
	out := orderport.ExportFacts{Customers: map[int64]orderport.ExportCustomer{}, Payments: map[int64]orderport.ExportPayment{}, Distributors: map[int64][]orderport.ExportDistributor{}, Commissions: map[int64]orderport.ExportCommission{}}
	customerSet := map[customerdomain.CustomerID]bool{}
	var orderIDs []int64
	distribution := map[int64][]distributionport.OrderDistributionLine{}
	for _, o := range orders {
		orderIDs = append(orderIDs, o.ID)
		if o.PayerCustomerID != nil {
			customerSet[customerdomain.CustomerID(*o.PayerCustomerID)] = true
		}
	}
	for start := 0; start < len(orderIDs); start += 200 {
		batch := orderIDs[start:min(start+200, len(orderIDs))]
		p, err := a.payments.ReadOrderExportPaymentsWithin(ctx, batch)
		if err != nil {
			return out, err
		}
		for id, v := range p {
			out.Payments[id] = orderport.ExportPayment{PaidAt: v.PaidAt, TransactionNo: v.TransactionNo, AmountMinor: v.AmountMinor, SharedMinor: v.SharedMinor, SharingProcessing: v.SharingProcessing, SharingUnknown: v.SharingUnknown, SharingException: v.SharingException}
		}
		d, err := a.distribution.ReadOrderDistribution(ctx, batch)
		if err != nil {
			return out, err
		}
		for id, lines := range d {
			distribution[id] = lines
			for _, line := range lines {
				customerSet[line.DistributorCustomerID] = true
			}
			out.Commissions[id] = exportCommissionFacts(lines)
		}
	}
	var ids []customerdomain.CustomerID
	for id := range customerSet {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	roots := map[customerdomain.CustomerID]customerdomain.CustomerID{}
	for start := 0; start < len(ids); start += 200 {
		values, err := a.roots.CanonicalCustomerRoots(ctx, ids[start:min(start+200, len(ids))])
		if err != nil {
			return out, err
		}
		for id, root := range values {
			roots[id] = root
		}
	}
	rootSet := map[customerdomain.CustomerID]bool{}
	for _, root := range roots {
		rootSet[root] = true
	}
	var rootIDs []customerdomain.CustomerID
	for root := range rootSet {
		rootIDs = append(rootIDs, root)
	}
	sort.Slice(rootIDs, func(i, j int) bool { return rootIDs[i] < rootIDs[j] })
	customers := map[customerdomain.CustomerID]orderport.ExportCustomer{}
	for start := 0; start < len(rootIDs); start += 200 {
		batch := rootIDs[start:min(start+200, len(rootIDs))]
		numbers, err := a.numbers.CustomerPublicNumbers(ctx, batch)
		if err != nil {
			return out, err
		}
		names, err := a.names.DisplayNames(ctx, batch)
		if err != nil {
			return out, err
		}
		for _, id := range batch {
			customers[id] = orderport.ExportCustomer{Number: numbers[id], Name: names[id]}
		}
	}
	fallbackRoots := map[customerdomain.CustomerID]bool{}
	for _, id := range fallbackIDs {
		if root, ok := roots[customerdomain.CustomerID(id)]; ok {
			fallbackRoots[root] = true
		}
	}
	for _, root := range rootIDs {
		if !fallbackRoots[root] {
			continue
		}
		phone, found, err := a.phones.RevealPhone(ctx, root)
		if err != nil {
			return out, err
		}
		if found {
			v := customers[root]
			v.Phone = phone
			customers[root] = v
		}
	}
	for id, root := range roots {
		out.Customers[int64(id)] = customers[root]
	}
	for id, lines := range distribution {
		sort.Slice(lines, func(i, j int) bool {
			if lines[i].ItemLine != lines[j].ItemLine {
				return lines[i].ItemLine < lines[j].ItemLine
			}
			return lines[i].AttributionID < lines[j].AttributionID
		})
		for _, line := range lines {
			customer := out.Customers[int64(line.DistributorCustomerID)]
			out.Distributors[id] = append(out.Distributors[id], orderport.ExportDistributor{ItemLine: line.ItemLine, Number: line.DistributorPublicNo, Name: customer.Name})
		}
	}
	return out, nil
}

func exportCommissionFacts(lines []distributionport.OrderDistributionLine) orderport.ExportCommission {
	var out orderport.ExportCommission
	for _, line := range lines {
		if !line.HasCommission {
			continue
		}
		out.PayableMinor += line.CurrentPayableMinor
		if line.Status != "cancelled" && line.Status != "zero_commission" && line.Status != "paid" {
			out.PendingMinor += max(line.CurrentPayableMinor-line.PaidMinor, 0)
		}
		for _, adjustment := range line.Adjustments {
			if adjustment.Kind == "manual_recovery" {
				out.RecoveredMinor += adjustment.DeltaMinor
			}
		}
		if line.Status == "exception" {
			out.Exception = true
		}
		for _, exception := range line.Exceptions {
			if exception.Status == "open" || exception.Status == "querying" {
				// Scheduling warnings are not unsettled money.
				if exception.Kind != "settlement_deadline_imminent" {
					out.Exception = true
				}
			}
		}
	}
	return out
}
