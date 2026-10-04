// migrate-external-orders imports only frozen CSV history into existing OneID
// customers. No Provider, Payment, entitlement or automation writer is composed.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const sourceSystem = "external-order-csv"

var errReview = errors.New("identity or source changed; stop and confirm affected source row")
var decimalMoney = regexp.MustCompile(`^(0|[1-9][0-9]*)(?:\.([0-9]{1,2}))?$`)

type csvPaths []string

func (p *csvPaths) String() string     { return "CSV files" }
func (p *csvPaths) Set(s string) error { *p = append(*p, s); return nil }

type sourceRow struct {
	Key, Union, Phone, ProductCode, ProductName, Status, AfterSales string
	PaidAt                                                          *time.Time
	Amount, Refund                                                  int64
	Digest                                                          [32]byte
	FileIndex, Line                                                 int
}
type source struct {
	Rows              []sourceRow
	Input, Duplicates int
	Digest            [32]byte
}

func minor(s string) (int64, error) {
	m := decimalMoney.FindStringSubmatch(s)
	if m == nil {
		return 0, errors.New("invalid source amount")
	}
	whole, e := strconv.ParseInt(m[1], 10, 64)
	if e != nil || whole > (1<<63-1)/100 {
		return 0, errors.New("source amount overflow")
	}
	cents := int64(0)
	if m[2] != "" {
		v := m[2]
		if len(v) == 1 {
			v += "0"
		}
		cents, _ = strconv.ParseInt(v, 10, 64)
	}
	if whole > (1<<63-1-cents)/100 {
		return 0, errors.New("source amount overflow")
	}
	return whole*100 + cents, nil
}
func load(paths []string, scope string) (source, error) {
	result := source{}
	seen := map[string]sourceRow{}
	hashes := []string{}
	zone, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		return result, e
	}
	for fi, path := range paths {
		b, e := os.ReadFile(path)
		if e != nil {
			return result, errors.New("CSV unavailable")
		}
		hash := sha256.Sum256(b)
		hashes = append(hashes, hex.EncodeToString(hash[:]))
		reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(b), "\ufeff")))
		headers, e := reader.Read()
		if e != nil {
			return result, errors.New("invalid CSV header")
		}
		indices := map[string]int{}
		for i, h := range headers {
			if _, ok := indices[h]; ok {
				return result, errors.New("duplicate CSV column")
			}
			indices[h] = i
		}
		for _, h := range []string{"内部订单号", "用户UNION_ID", "买家手机号", "商品ID", "商品名称", "支付时间", "订单状态", "售后状态", "订单实收金额", "订单已成功退款金额（出）"} {
			if _, ok := indices[h]; !ok {
				return result, errors.New("required CSV column missing")
			}
		}
		for line := 2; ; line++ {
			values, e := reader.Read()
			if e == io.EOF {
				break
			}
			if e != nil {
				return result, fmt.Errorf("invalid CSV at file %d row %d", fi+1, line)
			}
			get := func(h string) string { return strings.TrimSpace(values[indices[h]]) }
			all := map[string]string{}
			for i, h := range headers {
				if h != "序号" {
					all[h] = strings.TrimSpace(values[i])
				}
			}
			raw, _ := json.Marshal(all)
			r := sourceRow{Key: get("内部订单号"), Union: get("用户UNION_ID"), Phone: get("买家手机号"), ProductCode: get("商品ID"), ProductName: get("商品名称"), Status: get("订单状态"), AfterSales: get("售后状态"), Digest: sha256.Sum256(raw), FileIndex: fi + 1, Line: line}
			if r.Key == "" || r.ProductCode == "" || r.ProductName == "" {
				return result, fmt.Errorf("missing order fields at file %d row %d", fi+1, line)
			}
			r.Amount, e = minor(get("订单实收金额"))
			if e != nil {
				return result, e
			}
			r.Refund, e = minor(get("订单已成功退款金额（出）"))
			if e != nil || r.Refund > r.Amount {
				return result, errors.New("invalid source refund")
			}
			if t := get("支付时间"); t != "" {
				paid, e := time.ParseInLocation("2006-01-02 15:04:05", t, zone)
				if e != nil {
					return result, errors.New("invalid source payment time")
				}
				r.PaidAt = &paid
			}
			if _, e = normalizedStatus(r); e != nil {
				return result, e
			}
			result.Input++
			if old, ok := seen[r.Key]; ok {
				if old.Digest != r.Digest {
					return result, fmt.Errorf("duplicate source content changed at file %d row %d", fi+1, line)
				}
				result.Duplicates++
				continue
			}
			seen[r.Key] = r
		}
	}
	for _, r := range seen {
		result.Rows = append(result.Rows, r)
	}
	sort.Slice(result.Rows, func(i, j int) bool { return result.Rows[i].Key < result.Rows[j].Key })
	sort.Strings(hashes)
	raw, _ := json.Marshal(struct {
		Schema, Scope string
		Files         []string
	}{"external-order-csv-v2-meaningful-orders-refund-phone-only", scope, hashes})
	result.Digest = sha256.Sum256(raw)
	return result, nil
}
func normalizedStatus(r sourceRow) (orderdomain.Status, error) {
	if r.Status != "已完成" && r.Status != "已发货" && r.Status != "已关闭" {
		return "", errors.New("unknown source order status")
	}
	if r.Refund > 0 {
		if r.Refund == r.Amount {
			return orderdomain.StatusRefunded, nil
		}
		return orderdomain.StatusPartiallyRefunded, nil
	}
	if r.Status == "已关闭" {
		return orderdomain.StatusClosed, nil
	}
	return orderdomain.StatusPaid, nil
}
func phoneReference(value string) (identitydomain.Reference, bool) {
	value = strings.NewReplacer("-", "", " ", "", "(", "", ")", "").Replace(strings.TrimSpace(value))
	cn := strings.TrimPrefix(value, "+86")
	scope := "phone:e164"
	if len(cn) == 11 && !strings.HasPrefix(cn, "+") {
		value = cn
		scope = "phone:cn11"
	}
	ref := identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: scope, Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "order"}
	_, err := identitydomain.Normalize(ref)
	return ref, err == nil
}

type decision struct {
	Action         string
	Customer       int64
	Method         string
	Fill, Conflict bool
}
type report struct {
	Mode                                   string `json:"mode"`
	RunKey                                 string `json:"run_key"`
	Manifest                               string `json:"manifest_sha256"`
	Plan                                   string `json:"plan_sha256"`
	Input                                  int    `json:"input_rows"`
	Unique                                 int    `json:"unique_orders"`
	Duplicates                             int    `json:"duplicate_rows"`
	Union, Phone, Skipped                  int
	PhoneFillCustomers                     int `json:"phone_fill_customers"`
	Conflicts                              int `json:"cross_customer_orders"`
	Zero                                   int `json:"zero_orders"`
	UnknownTime                            int `json:"unknown_time_orders"`
	RefundOrders                           int `json:"refund_orders"`
	Amount, Refund                         int64
	Imported, Replayed, Filled, Reconciled int
	ExcludedNoTime                         int `json:"excluded_no_payment_time"`
	ExcludedZero                           int `json:"excluded_zero_amount"`
	RefundSourceRows                       int `json:"refund_phone_only_source_rows"`
	RefundMatchedRows                      int `json:"refund_phone_only_matched_rows"`
	PhoneOnlyFilled                        int `json:"refund_phone_only_filled"`
	PhoneOnlyReconciled                    int `json:"refund_phone_only_reconciled"`
	FailedFile                             int `json:"failed_file,omitempty"`
	FailedRow                              int `json:"failed_row,omitempty"`
}
type matcher struct {
	Resolver  identityport.Resolver
	Verifier  identityport.VerifiedScopedUnionReader
	Directory identityport.DirectoryIdentityReader
	Displays  customerport.DirectoryContactDisplayReader
	Scope     string
}

func (m matcher) choose(ctx context.Context, r sourceRow) (decision, error) {
	d := decision{Method: "skipped"}
	var u, p identityport.ResolveResult
	var err error
	if r.Union != "" {
		ref := identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: m.Scope, Value: r.Union, Assurance: identitydomain.AssuranceDeclared, Source: "order"}
		if _, e := identitydomain.Normalize(ref); e == nil {
			u, err = m.Resolver.Resolve(ctx, ref)
			if err != nil {
				return d, err
			}
		}
		if u.Status == identityport.ResolveConflict {
			return d, errReview
		}
		if u.Status == identityport.ResolveFound {
			ok, e := m.Verifier.HasVerifiedScopedUnion(ctx, u.CustomerID, m.Scope, r.Union)
			if e != nil {
				return d, e
			}
			if !ok {
				return d, errReview
			}
		}
	}
	ref, valid := phoneReference(r.Phone)
	if valid {
		p, err = m.Resolver.Resolve(ctx, ref)
		if err != nil {
			return d, err
		}
	}
	if u.Status == identityport.ResolveFound {
		d.Customer = int64(u.CustomerID)
		d.Method = "union"
		d.Conflict = p.Status == identityport.ResolveConflict || p.Status == identityport.ResolveFound && p.CustomerID != u.CustomerID
	} else if p.Status == identityport.ResolveConflict {
		return d, errReview
	} else if p.Status == identityport.ResolveFound {
		d.Customer = int64(p.CustomerID)
		d.Method = "phone"
	}
	if d.Method == "union" && valid && ref.Scope == "phone:cn11" && !d.Conflict {
		_, phones, e := m.Directory.DirectoryIdentities(ctx, customerdomain.CustomerID(d.Customer))
		if e != nil {
			return d, e
		}
		displays, e := m.Displays.ContactDisplays(ctx, []customerdomain.CustomerID{customerdomain.CustomerID(d.Customer)})
		if e != nil {
			return d, e
		}
		empty := len(phones) == 0
		for _, v := range displays {
			if v.PhoneMasked != "" {
				empty = false
			}
		}
		d.Fill = empty
	}
	return d, nil
}
func phoneCommand(r sourceRow, customer int64, at time.Time) customerport.CollectedPhoneCommand {
	ref, _ := phoneReference(r.Phone)
	key := sha256.Sum256([]byte(sourceSystem + "\x00" + r.Key))
	return customerport.CollectedPhoneCommand{DeclaredPhoneCommand: identityport.DeclaredPhoneCommand{OnlyIfEmpty: true, CustomerID: customerdomain.CustomerID(customer), Phone: ref.Value, Source: "order", SourceEventID: "external-csv:" + hex.EncodeToString(r.Digest[:]), IdempotencyKey: "external-csv-phone:" + hex.EncodeToString(key[:])}, CollectedAt: at, Historical: true}
}
func verifyPhoneReceipt(ctx context.Context, m matcher, r sourceRow, d decision, reconcile bool) error {
	reader, ok := m.Resolver.(identityport.DeclaredPhoneReceiptReader)
	if !ok {
		return errReview
	}
	command := phoneCommand(r, d.Customer, time.Now().UTC())
	receipt, found, e := reader.ReadDeclaredPhoneReceiptWithin(ctx, command.DeclaredPhoneCommand)
	if e != nil {
		return e
	}
	if !found {
		if reconcile && d.Fill {
			return errors.New("missing historical phone fill")
		}
		return nil
	}
	if receipt.Status != identityport.DeclaredAttached || int64(receipt.CustomerID) != d.Customer || receipt.IdentityID < 1 {
		return errReview
	}
	ref, valid := phoneReference(r.Phone)
	if !valid {
		return errReview
	}
	p, e := m.Resolver.Resolve(ctx, ref)
	if e != nil {
		return e
	}
	if p.Status != identityport.ResolveFound || p.CustomerID != receipt.CustomerID {
		return errReview
	}
	displays, e := m.Displays.ContactDisplays(ctx, []customerdomain.CustomerID{receipt.CustomerID})
	if e != nil {
		return e
	}
	display, ok := displays[receipt.CustomerID]
	if !ok || len(ref.Value) != 11 || display.PhoneMasked != ref.Value[:3]+"****"+ref.Value[7:] {
		return errReview
	}
	return nil
}
func action(r sourceRow) string {
	if r.Refund > 0 || r.AfterSales == "全额退款" || r.AfterSales == "部分退款" {
		return "phone_only"
	}
	if r.PaidAt == nil {
		return "excluded_no_time"
	}
	if r.Amount == 0 {
		return "excluded_zero"
	}
	return "order"
}
func inspect(ctx context.Context, uow platformport.UnitOfWork, m matcher, orders orderport.ExternalHistoryRepository, src source) (report, []decision, error) {
	out := report{Input: src.Input, Unique: len(src.Rows), Duplicates: src.Duplicates, Manifest: hex.EncodeToString(src.Digest[:]), RunKey: "external-csv-" + hex.EncodeToString(src.Digest[:12])}
	decisions := make([]decision, 0, len(src.Rows))
	fills := map[int64]string{}
	phoneCustomers := map[string]int64{}
	err := uow.Within(ctx, func(tx context.Context) error {
		cache := map[string]decision{}
		for _, r := range src.Rows {
			a := action(r)
			if a == "excluded_no_time" || a == "excluded_zero" {
				decisions = append(decisions, decision{Action: a, Method: "excluded"})
				if a == "excluded_no_time" {
					out.ExcludedNoTime++
				} else {
					out.ExcludedZero++
				}
				continue
			}
			if a == "phone_only" {
				out.RefundSourceRows++
			}
			cacheKey := r.Union + "\x00" + r.Phone
			d, ok := cache[cacheKey]
			if !ok {
				var e error
				d, e = m.choose(tx, r)
				if e != nil {
					out.FailedFile = r.FileIndex
					out.FailedRow = r.Line
					return e
				}
				cache[cacheKey] = d
			}
			d.Action = a
			if e := verifyPhoneReceipt(tx, m, r, d, false); e != nil {
				out.FailedFile = r.FileIndex
				out.FailedRow = r.Line
				return e
			}
			existing, found, e := orders.LookupExternalHistoryWithin(tx, sourceSystem, r.Key, r.Digest)
			if e != nil {
				out.FailedFile = r.FileIndex
				out.FailedRow = r.Line
				return e
			}
			if found && (d.Customer == 0 || existing.PayerCustomerID == nil || *existing.PayerCustomerID != d.Customer) {
				out.FailedFile = r.FileIndex
				out.FailedRow = r.Line
				return errReview
			}
			decisions = append(decisions, d)
			if d.Action == "order" {
				switch d.Method {
				case "union":
					out.Union++
				case "phone":
					out.Phone++
				default:
					out.Skipped++
				}
			}
			if d.Action == "phone_only" && d.Customer != 0 {
				out.RefundMatchedRows++
			}
			if d.Action == "order" && d.Customer != 0 {
				out.Amount += r.Amount
				out.Refund += r.Refund
				if r.Amount == 0 {
					out.Zero++
				}
				if r.PaidAt == nil {
					out.UnknownTime++
				}
				if r.Refund > 0 {
					out.RefundOrders++
				}
			}
			if d.Fill {
				ref, _ := phoneReference(r.Phone)
				priorPhone, hasPhone := fills[d.Customer]
				priorCustomer, hasCustomer := phoneCustomers[ref.Value]
				if hasPhone && priorPhone != ref.Value || hasCustomer && priorCustomer != d.Customer {
					out.FailedFile = r.FileIndex
					out.FailedRow = r.Line
					return errReview
				}
				fills[d.Customer] = ref.Value
				phoneCustomers[ref.Value] = d.Customer
			}
			if d.Conflict {
				out.Conflicts++
			}
		}
		return nil
	})
	out.PhoneFillCustomers = len(fills)
	raw, _ := json.Marshal(struct {
		Manifest  string
		Decisions []decision
	}{out.Manifest, decisions})
	hash := sha256.Sum256(raw)
	out.Plan = hex.EncodeToString(hash[:])
	return out, decisions, err
}
func snapshot(r sourceRow, d decision, at time.Time) orderdomain.Snapshot {
	created := at
	if r.PaidAt != nil {
		created = r.PaidAt.UTC()
	}
	status, _ := normalizedStatus(r)
	id := d.Customer
	return orderdomain.Snapshot{Provider: orderdomain.ProviderExternalHistory, SourceSystem: sourceSystem, SourceKey: r.Key, MerchantOrderNo: r.Key, PayerCustomerID: &id, BeneficiaryCustomerID: &id, Amount: orderdomain.Money{AmountMinor: r.Amount, Currency: "CNY"}, RefundedMinor: r.Refund, Status: status, Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductCode: r.ProductCode, ProductName: r.ProductName, Quantity: 1, UnitAmountMinor: r.Amount, LineAmountMinor: r.Amount}}, RecordOrigin: orderdomain.RecordOriginHistory, Version: 1, CreatedAt: created, UpdatedAt: created, ExternalHistory: &orderdomain.ExternalHistoryFacts{SourcePaidAt: r.PaidAt, ImportedAt: at, SourceStatus: r.Status, SourceAfterSales: r.AfterSales}}
}
func sameHistoricalFacts(prior orderdomain.Snapshot, row sourceRow, d decision) bool {
	expected := snapshot(row, d, time.Now().UTC())
	if prior.Provider != expected.Provider || prior.RecordOrigin != expected.RecordOrigin || prior.SourceSystem != sourceSystem || prior.SourceKey != row.Key || prior.MerchantOrderNo != row.Key || prior.PayerCustomerID == nil || *prior.PayerCustomerID != d.Customer || prior.BeneficiaryCustomerID == nil || *prior.BeneficiaryCustomerID != d.Customer || prior.Amount != expected.Amount || prior.RefundedMinor != row.Refund || prior.Status != expected.Status || prior.EffectEligible || prior.ProviderTransactionNo != "" || prior.ExternalHistory == nil || prior.ExternalHistory.ImportedAt.IsZero() || prior.ExternalHistory.SourceStatus != row.Status || prior.ExternalHistory.SourceAfterSales != row.AfterSales || len(prior.Items) != 1 || prior.Items[0] != expected.Items[0] {
		return false
	}
	a, b := prior.ExternalHistory.SourcePaidAt, row.PaidAt
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}
func apply(ctx context.Context, uow platformport.UnitOfWork, m matcher, orders orderport.ExternalHistoryRepository, phones customerport.CollectedPhoneRecorder, src source, decisions []decision, out *report, mode string) error {
	at := time.Now().UTC()
	for i, r := range src.Rows {
		want := decisions[i]
		if want.Customer == 0 {
			continue
		}
		var imported, replayed, filled, reconciled, phoneOnlyFilled, phoneOnlyReconciled int
		err := uow.Within(ctx, func(tx context.Context) error {
			if mode == "apply" {
				locker, ok := m.Resolver.(identityport.CustomerIdentityLocker)
				if !ok {
					return errReview
				}
				if e := locker.LockActiveCustomerWithin(tx, customerdomain.CustomerID(want.Customer)); e != nil {
					return e
				}
			}
			current, e := m.choose(tx, r)
			if e != nil {
				return e
			}
			if current.Customer != want.Customer || current.Method != want.Method || current.Conflict != want.Conflict {
				return errReview
			}
			if e := verifyPhoneReceipt(tx, m, r, current, mode == "reconcile"); e != nil {
				return e
			}
			if want.Action == "phone_only" {
				if mode == "reconcile" {
					phoneOnlyReconciled++
					return nil
				}
				if current.Fill {
					bound, e := phones.CollectPhoneWithin(tx, phoneCommand(r, want.Customer, at))
					if e != nil {
						return e
					}
					if bound.Status == identityport.DeclaredConflict {
						return errReview
					}
					if bound.Status == identityport.DeclaredAttached {
						filled++
						phoneOnlyFilled++
					}
				}
				return nil
			}
			prior, found, e := orders.LookupExternalHistoryWithin(tx, sourceSystem, r.Key, r.Digest)
			if e != nil {
				return e
			}
			if found {
				if !sameHistoricalFacts(prior, r, want) {
					return errReview
				}
				if mode == "reconcile" {
					reconciled++
				} else {
					if _, _, e := orders.ImportHistoricalWithin(tx, orderport.HistoricalImportCommand{RunID: out.RunKey, SourceDigest: r.Digest, Order: prior}); e != nil {
						return e
					}
					replayed++
				}
				return nil
			}
			if mode == "reconcile" {
				return errors.New("missing imported order")
			}
			if current.Fill {
				bound, e := phones.CollectPhoneWithin(tx, phoneCommand(r, want.Customer, at))
				if e != nil {
					return e
				}
				if bound.Status == identityport.DeclaredConflict {
					return errReview
				}
				if bound.Status == identityport.DeclaredAttached {
					filled++
				}
			}
			_, created, e := orders.ImportHistoricalWithin(tx, orderport.HistoricalImportCommand{RunID: out.RunKey, SourceDigest: r.Digest, Order: snapshot(r, want, at)})
			if e != nil {
				return e
			}
			if created {
				imported++
			} else {
				replayed++
			}
			return nil
		})
		if err != nil {
			out.FailedFile = r.FileIndex
			out.FailedRow = r.Line
			return err
		}
		out.Imported += imported
		out.Replayed += replayed
		out.Filled += filled
		out.Reconciled += reconciled
		out.PhoneOnlyFilled += phoneOnlyFilled
		out.PhoneOnlyReconciled += phoneOnlyReconciled
	}
	return nil
}
func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-external-orders", flag.ContinueOnError)
	var paths csvPaths
	flags.Var(&paths, "csv", "source CSV path (repeat for all three files)")
	mode := flags.String("mode", "inspect", "inspect|apply|reconcile")
	scope := flags.String("union-scope", "", "existing verified Open Platform scope")
	expected := flags.String("manifest-sha256", "", "exact source manifest digest")
	plan := flags.String("plan-sha256", "", "exact read-only matching plan digest")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(paths) == 0 || (*mode != "inspect" && *mode != "apply" && *mode != "reconcile") || identitydomain.ValidateNamespace(identitydomain.KindUnionID, *scope) != nil {
		return errors.New("invalid import arguments")
	}
	src, err := load(paths, *scope)
	if err != nil {
		return err
	}
	if *mode != "inspect" && *expected != hex.EncodeToString(src.Digest[:]) {
		return errors.New("source manifest mismatch")
	}
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: url, MaxConnections: 2})
	if err != nil {
		return errors.New("target database unavailable")
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	ro, err := platformpostgres.NewReadOnlyRepeatableReadUnitOfWork(pool)
	if err != nil {
		return err
	}
	key, err := platformconfig.IdentityPhoneDataKey()
	if err != nil {
		return err
	}
	vault, err := identitysecure.NewPhoneVault(key)
	if err != nil {
		return err
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore(vault)}
	directory := identityquery.PostgreSQL{}
	customers := customerstore.NewPostgreSQL()
	orders, err := orderstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	phones, err := customerapp.NewCollectedPhoneService(oneID, customers)
	if err != nil {
		return err
	}
	m := matcher{Resolver: oneID, Verifier: directory, Directory: directory, Displays: customers, Scope: *scope}
	out, decisions, err := inspect(ctx, ro, m, orders, src)
	out.Mode = *mode
	if err == nil && *mode == "apply" && *plan != out.Plan {
		err = errors.New("matching plan changed; inspect and confirm new ambiguity before apply")
	}
	if err == nil && *mode != "inspect" {
		if *mode == "apply" {
			err = (ordermigration.PostgreSQLRuns{Pool: pool.Native()}).Begin(ctx, out.RunKey, src.Digest, int64(out.Union+out.Phone))
		}
		if err == nil {
			runner := platformport.UnitOfWork(uow)
			if *mode == "reconcile" {
				runner = ro
			}
			err = apply(ctx, runner, m, orders, phones, src, decisions, &out, *mode)
		}
		if err == nil && *mode == "apply" {
			err = (ordermigration.PostgreSQLRuns{Pool: pool.Native()}).CompleteOrders(ctx, out.RunKey, int64(out.Union+out.Phone))
		}
	}
	if err != nil {
		out.Plan = ""
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
	if err != nil {
		return errors.New("external order import stopped; preserve original receipt and inspect reported source row")
	}
	return nil
}
func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
