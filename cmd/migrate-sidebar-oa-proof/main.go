package main

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	ia "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	app "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	proof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/oaproof"
	ip "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	store "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	pay "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	pp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"net/http"
	"os"
	"time"
)

type noWrites struct{}

func (noWrites) Load(context.Context, effect.Kind, effect.Digest) (pp.Material, error) {
	return pp.Material{}, errors.New("read only")
}
func main() {
	if e := run(context.Background(), os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "OA migration failed:", e)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("oa-proof", flag.ContinueOnError)
	mode := f.String("mode", "verify", "verify|apply")
	input := f.String("source", "", "protected source")
	path := f.String("proof", "", "encrypted proof")
	key := f.String("key-file", "", "protected key")
	want := f.String("sha256", "", "exact source/proof digest")
	confirm := f.Bool("confirm-provision", false, "explicit OA-only provisioning")
	if e := f.Parse(args); e != nil {
		return e
	}
	cfg, e := config.Load()
	if e != nil {
		return errors.New("configuration unavailable")
	}
	if *mode == "verify" {
		i, e := os.Lstat(*input)
		if e != nil || !i.Mode().IsRegular() || i.Mode().Perm() != 0600 || i.Size() > 1<<20 {
			return errors.New("protected source required")
		}
		b, e := os.ReadFile(*input)
		if e != nil {
			return e
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != *want {
			return errors.New("source digest mismatch")
		}
		var source struct {
			Version int         `json:"version"`
			Rows    []proof.Row `json:"rows"`
		}
		if json.Unmarshal(b, &source) != nil || source.Version != 1 || len(source.Rows) != 3 {
			return errors.New("invalid source")
		}
		private, e := os.ReadFile(cfg.WeChatPay.PrivateKeyPath)
		if e != nil {
			return errors.New("private key unavailable")
		}
		cert, e := os.ReadFile(cfg.WeChatPay.PlatformCertPath)
		if e != nil {
			return errors.New("platform key unavailable")
		}
		signer, e := pp.ParseMerchantPrivateKey(private)
		if e != nil {
			return e
		}
		serial, pub, e := pp.ParsePlatformCertificate(cert)
		if e != nil {
			return e
		}
		client, e := pp.NewWeChatPay(pp.Config{Enabled: true, AppID: cfg.WeChatPay.AppID, AppScope: cfg.WeChatPay.AppScope, APIBaseURL: "https://api.mch.weixin.qq.com", PaymentNotifyURL: "https://www.youcangogogo.com/api/public/wechat-pay/callbacks/payment", RefundNotifyURL: "https://www.youcangogogo.com/api/public/wechat-pay/callbacks/refund", Credential: pp.Credential{MerchantID: cfg.WeChatPay.MerchantID, Serial: cfg.WeChatPay.MerchantSerial, Signer: signer, PlatformKeys: map[string]*rsa.PublicKey{serial: pub}}}, noWrites{}, &http.Client{Timeout: 15 * time.Second})
		if e != nil {
			return errors.New("provider config invalid")
		}
		out := proof.Snapshot{Version: 1, SourceSHA256: *want, VerifiedAt: time.Now().UTC(), Rows: source.Rows}
		for n, r := range source.Rows {
			if r.AppID != cfg.WeChatPay.H5AppID || r.OpenID == "" {
				return errors.New("source OA scope mismatch")
			}
			q, e := client.QueryPayment(ctx, r.MerchantOrderNo)
			if e != nil {
				return errors.New("signed query failed")
			}
			if !matches(r, q) {
				return errors.New("signed query source mismatch")
			}
			out.Rows[n].EvidenceDigest = string(q.EvidenceDigest)
		}
		d, e := proof.Seal(out, *path, *key)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"verified_payment_rows": 3, "subjects": 2, "proof_sha256": hex.EncodeToString(d[:]), "identity_writes": 0})
	}
	if *mode != "apply" || !*confirm {
		return errors.New("explicit provisioning required")
	}
	s, d, e := proof.Load(*path, *key)
	if e != nil {
		return e
	}
	if *want != hex.EncodeToString(d[:]) || time.Since(s.VerifiedAt) > time.Hour || s.VerifiedAt.After(time.Now().Add(time.Minute)) {
		return errors.New("stale or mismatched live proof")
	}
	refs, e := s.References()
	if e != nil {
		return e
	}
	url, e := config.DatabaseURL()
	if e != nil {
		return e
	}
	pool, e := pg.Open(ctx, pg.Config{URL: url, MaxConnections: 2, MinConnections: 1})
	if e != nil {
		return errors.New("target connection")
	}
	defer pool.Close()
	uow, e := pg.NewUnitOfWork(pool)
	if e != nil {
		return e
	}
	owner := app.OneIDService{Store: store.NewPostgresStore()}
	counts := map[string]int{}
	e = uow.Within(ctx, func(tx context.Context) error {
		for _, ref := range refs {
			if ref.Scope != "wechat-app:"+cfg.WeChatPay.H5AppID {
				return errors.New("target OA scope changed")
			}
			found, e := owner.Resolve(tx, ref)
			if e != nil {
				return e
			}
			if found.Status == ip.ResolveFound {
				counts["already_found"]++
				continue
			}
			if found.Status != ip.ResolveNotFound {
				return errors.New("identity conflict")
			}
			fact, e := (ia.ProviderHistory{}).VerifiedHistoricalFact(ip.HistoricalVerifiedInput{Kind: string(ref.Kind), Scope: ref.Scope, Value: ref.Value, Source: ref.Source})
			if e != nil {
				return e
			}
			if _, e = owner.ProvisionCustomerFromVerifiedIdentity(tx, fact); e != nil {
				return e
			}
			counts["provisioned"]++
		}
		return nil
	})
	if e != nil {
		return errors.New("OA provisioning rolled back")
	}
	return json.NewEncoder(os.Stdout).Encode(counts)
}
func matches(r proof.Row, q pay.WeChatPayPaymentQuery) bool {
	return q.Status == "SUCCESS" && q.AppID == r.AppID && q.PayerOpenID == r.OpenID && q.AmountMinor == r.AmountMinor && q.Currency == "CNY" && q.MerchantOrderNo == r.MerchantOrderNo && q.EvidenceDigest != ""
}
