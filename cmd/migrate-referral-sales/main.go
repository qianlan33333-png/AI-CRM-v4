// Command migrate-referral-sales previews and applies Referral-only sales
// evidence from Order's immutable paid/refund facts and Distribution's frozen
// attribution. It never calls Payment or Distribution settlement consumers.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	referralapp "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/app"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
)

type decision struct {
	OrderID            int64  `json:"order_id"`
	State              string `json:"state"`
	Reason             string `json:"reason,omitempty"`
	CampaignID         int64  `json:"campaign_id,omitempty"`
	PromoterCustomerID int64  `json:"promoter_customer_id,omitempty"`
	AmountMinor        int64  `json:"gross_amount_minor,omitempty"`
	RefundCount        int    `json:"refund_count,omitempty"`
	Already            bool   `json:"already_credited,omitempty"`
	EvidenceSHA256     string `json:"evidence_sha256,omitempty"`
}

type report struct {
	Mode              string     `json:"mode"`
	ManifestSHA256    string     `json:"manifest_sha256"`
	PaidOrdersScanned int        `json:"paid_orders_scanned"`
	Candidates        int        `json:"attributed_candidates"`
	Eligible          int        `json:"eligible"`
	AlreadyCredited   int        `json:"already_credited"`
	Applied           int        `json:"applied"`
	RefundsChecked    int        `json:"refund_settlements_checked"`
	Exceptions        []decision `json:"exceptions"`
	Rows              []decision `json:"rows"`
}

type closeEnqueuer struct{}

func (closeEnqueuer) EnqueueCampaignCloseWithin(context.Context, int64, time.Time) error {
	return errors.New("campaign close is unavailable in backfill")
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "referral sales backfill failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-referral-sales", flag.ContinueOnError)
	mode := flags.String("mode", "dry-run", "dry-run|apply")
	digest := flags.String("manifest-sha256", "", "digest from the reviewed dry-run")
	confirm := flags.Bool("confirm-apply", false, "confirm applying the reviewed manifest")
	orderID := flags.Int64("order-id", 0, "optional exact order for a bounded run")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*mode != "dry-run" && *mode != "apply") || *orderID < 0 {
		return errors.New("invalid mode or order-id")
	}
	if *mode == "apply" && (!*confirm || len(*digest) != 64) {
		return errors.New("apply requires --confirm-apply and --manifest-sha256 from dry-run")
	}
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: url, MinConnections: 1, MaxConnections: 5})
	if err != nil {
		return err
	}
	defer pool.Close()
	readUOW, err := platformpostgres.NewReadOnlyRepeatableReadUnitOfWork(pool)
	if err != nil {
		return err
	}
	writeUOW, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	orders, err := orderstore.NewPostgreSQL(pool.Native(), readUOW)
	if err != nil {
		return err
	}
	distribution, err := distributionstore.NewPostgreSQL(pool.Native(), readUOW)
	if err != nil {
		return err
	}
	referrals, err := referralstore.NewPostgreSQL(pool.Native(), writeUOW)
	if err != nil {
		return err
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		return err
	}
	tokenDataKey, err := platformconfig.ReferralTokenDataKey()
	if err != nil {
		return err
	}
	service, err := referralapp.NewService(writeUOW, referrals, "https://www.youcangogogo.com", tokenDataKey, closeEnqueuer{}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		return err
	}
	if err = service.SetSaleEvidenceReaders(distributionapp.NewFrozenAttributionReader(distribution), platformaudit.NewPostgreSQLStore()); err != nil {
		return err
	}
	reader := distributionapp.NewFrozenAttributionReader(distribution)
	result, candidates, err := preview(ctx, readUOW, orders, reader, service, *orderID)
	if err != nil {
		return err
	}
	result.Mode = *mode
	if *mode == "apply" {
		provided, decodeErr := hex.DecodeString(*digest)
		computed, _ := hex.DecodeString(result.ManifestSHA256)
		if decodeErr != nil || subtle.ConstantTimeCompare(provided, computed) != 1 {
			return errors.New("preview manifest changed; run dry-run again")
		}
		for _, candidate := range candidates {
			var credited bool
			err = writeUOW.Within(ctx, func(tx context.Context) error {
				fact, readErr := orders.ReadPaidSaleBackfillFactWithin(tx, candidate.OrderID)
				if readErr != nil {
					return readErr
				}
				fresh, readErr := service.PreviewAttributedSaleWithin(tx, fact.Paid)
				if readErr != nil || !fresh.Eligible || fresh.CampaignID != candidate.CampaignID || fresh.PromoterCustomerID != candidate.PromoterCustomerID || fresh.GrossAmountMinor != candidate.AmountMinor || len(fact.Refunds) != candidate.RefundCount || evidenceDigest(fact) != candidate.EvidenceSHA256 {
					return errors.New("candidate changed during apply: " + strconv.FormatInt(candidate.OrderID, 10))
				}
				if !fresh.AlreadyCredited {
					if err := service.BackfillAttributedSaleWithin(tx, fact.Paid); err != nil {
						return err
					}
					credited = true
				}
				for _, refund := range fact.Refunds {
					if err := service.ConsumeRefundSettlementWithin(tx, refund); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return fmt.Errorf("order %d: %w", candidate.OrderID, err)
			}
			if credited {
				result.Applied++
			} else if !candidate.Already {
				result.AlreadyCredited++
			}
			result.RefundsChecked += candidate.RefundCount
		}
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func preview(ctx context.Context, uow platformport.UnitOfWork, orders orderport.PaidSaleBackfillReader, attributions distributionport.FrozenOrderAttributionReader, service *referralapp.Service, exactOrderID int64) (report, []decision, error) {
	result := report{Rows: []decision{}, Exceptions: []decision{}}
	var candidates []decision
	ids := []int64{}
	if exactOrderID > 0 {
		ids = append(ids, exactOrderID)
	} else {
		var after int64
		for {
			var page []int64
			if err := uow.Within(ctx, func(tx context.Context) error {
				var readErr error
				page, readErr = orders.ListPaidSaleOrderIDsWithin(tx, after, 500)
				return readErr
			}); err != nil {
				return report{}, nil, err
			}
			ids = append(ids, page...)
			if len(page) < 500 {
				break
			}
			after = page[len(page)-1]
		}
	}
	hash := sha256.New()
	for _, id := range ids {
		result.PaidOrdersScanned++
		row := decision{OrderID: id}
		err := uow.Within(ctx, func(tx context.Context) error {
			_, readErr := attributions.ReadFrozenOrderAttributionWithin(tx, id, 1)
			if errors.Is(readErr, distributionport.ErrNotFound) {
				row.State = "no_distribution_attribution"
				return nil
			}
			row.State = "exception"
			if readErr != nil {
				row.Reason = "frozen_distribution_attribution: " + readErr.Error()
				return nil
			}
			result.Candidates++
			fact, readErr := orders.ReadPaidSaleBackfillFactWithin(tx, id)
			if readErr != nil {
				row.Reason = "order_paid_or_refund_evidence: " + readErr.Error()
				return nil
			}
			row.EvidenceSHA256 = evidenceDigest(fact)
			candidate, readErr := service.PreviewAttributedSaleWithin(tx, fact.Paid)
			if readErr != nil {
				row.Reason = "referral_eligibility: " + readErr.Error()
				return nil
			}
			if !candidate.Eligible {
				row.State = "outside_active_campaign"
				return nil
			}
			row.State, row.CampaignID, row.PromoterCustomerID, row.AmountMinor, row.RefundCount, row.Already = "eligible", candidate.CampaignID, candidate.PromoterCustomerID, candidate.GrossAmountMinor, len(fact.Refunds), candidate.AlreadyCredited
			return nil
		})
		if err != nil {
			return report{}, nil, err
		}
		if row.State == "eligible" {
			result.Eligible++
			if row.Already {
				result.AlreadyCredited++
			}
			candidates = append(candidates, row)
		} else if row.State == "exception" {
			result.Exceptions = append(result.Exceptions, row)
		}
		if row.State != "no_distribution_attribution" {
			result.Rows = append(result.Rows, row)
		}
		encoded, _ := json.Marshal(row)
		_, _ = hash.Write(encoded)
		_, _ = hash.Write([]byte{10})
	}
	result.ManifestSHA256 = hex.EncodeToString(hash.Sum(nil))
	return result, candidates, nil
}

func evidenceDigest(fact orderport.PaidSaleBackfillFact) string {
	encoded, _ := json.Marshal(fact)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
