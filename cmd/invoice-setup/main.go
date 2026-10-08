// invoice-setup prepares and checks the official invoice configuration. It never
// issues invoices, reads customer orders, or changes payment credentials.
package main

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	provider "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/provider"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

type setupConfig struct {
	ServiceMerchantID       string `json:"service_merchant_id"`
	SubMerchantID           string `json:"sub_merchant_id"`
	MerchantCertificatePath string `json:"merchant_certificate_path"`
	PrivateKeyPath          string `json:"private_key_path"`
	AuthMode                string `json:"auth_mode"`
	PlatformCertificatePath string `json:"platform_certificate_path,omitempty"`
	PublicKeyID             string `json:"public_key_id,omitempty"`
	PublicKeyPath           string `json:"public_key_path,omitempty"`
	PublicOrigin            string `json:"public_origin"`
	CardAppID               string `json:"card_appid"`
	CardLogoURL             string `json:"card_logo_url"`
}

func (c setupConfig) valid() bool {
	u, e := url.Parse(c.PublicOrigin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Opaque != "" {
		return false
	}
	for _, v := range []string{c.ServiceMerchantID, c.SubMerchantID, c.MerchantCertificatePath, c.PrivateKeyPath} {
		if v == "" || strings.TrimSpace(v) != v {
			return false
		}
	}
	if strings.Trim(c.ServiceMerchantID, "0123456789") != "" || strings.Trim(c.SubMerchantID, "0123456789") != "" {
		return false
	}
	switch c.AuthMode {
	case "public_key":
		return c.PlatformCertificatePath == "" && strings.HasPrefix(c.PublicKeyID, "PUB_KEY_ID_") && c.PublicKeyPath != ""
	case "certificate":
		return c.PublicKeyID == "" && c.PublicKeyPath == "" && c.PlatformCertificatePath != ""
	default:
		return false
	}
}
func (c setupConfig) development() provider.DevelopmentConfig {
	// The CRM flow uses its own application IDs. The payment-bill entry must stay
	// off until that independent entry has an ownership/transaction mapping.
	return provider.DevelopmentConfig{CallbackURL: c.PublicOrigin + "/api/callbacks/wechat-invoice", SubMerchant: c.SubMerchantID, ShowInvoiceCell: false, SupportVAT: false}
}
func (c setupConfig) card() (provider.CardTemplate, bool) {
	var out provider.CardTemplate
	u, e := url.Parse(c.CardLogoURL)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() != "mmbiz.qpic.cn" || u.User != nil || u.Fragment != "" || c.CardAppID == "" {
		return out, false
	}
	out.SubMerchant = c.SubMerchantID
	out.AppID = c.CardAppID
	out.Information.LogoURL = c.CardLogoURL
	out.Information.CustomCell.Words = "电子发票"
	out.Information.CustomCell.Description = "查看我的订单"
	out.Information.CustomCell.JumpURL = c.PublicOrigin + "/my/orders"
	return out, true
}

type receipt struct {
	Action              string                  `json:"action"`
	RequestedAt         time.Time               `json:"requested_at"`
	Request             any                     `json:"request,omitempty"`
	Calls               []provider.SetupReceipt `json:"calls"`
	Complete            bool                    `json:"complete"`
	IndependentReadback bool                    `json:"independent_readback"`
}

func readConfig(path string) (setupConfig, error) {
	var c setupConfig
	f, e := os.Open(path)
	if e != nil {
		return c, fmt.Errorf("setup config unavailable")
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 16<<10 {
		return c, fmt.Errorf("setup config must be a private regular file")
	}
	d := json.NewDecoder(io.LimitReader(f, 16<<10))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || !c.valid() {
		return c, fmt.Errorf("setup config incomplete")
	}
	return c, nil
}
func client(ctx context.Context, c setupConfig) (provider.SetupClient, error) {
	cert, e := utils.LoadCertificateWithPath(c.MerchantCertificatePath)
	if e != nil || cert.Subject.CommonName != c.ServiceMerchantID || time.Now().Before(cert.NotBefore) || time.Now().After(cert.NotAfter) {
		return provider.SetupClient{}, fmt.Errorf("service merchant certificate invalid")
	}
	key, e := utils.LoadPrivateKeyWithPath(c.PrivateKeyPath)
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if e != nil || !ok || !pub.Equal(&key.PublicKey) {
		return provider.SetupClient{}, fmt.Errorf("service merchant signing key mismatch")
	}
	trust := provider.Trust{Mode: c.AuthMode, PublicKeyID: c.PublicKeyID}
	if c.AuthMode == "public_key" {
		trust.PublicKey, e = utils.LoadPublicKeyWithPath(c.PublicKeyPath)
	} else {
		trust.Certificate, e = utils.LoadCertificateWithPath(c.PlatformCertificatePath)
	}
	if e != nil {
		return provider.SetupClient{}, fmt.Errorf("invoice trust material unavailable")
	}
	sdk, verifier, _, e := provider.NewTrustedClient(ctx, c.ServiceMerchantID, strings.ToUpper(cert.SerialNumber.Text(16)), key, trust)
	if e != nil {
		return provider.SetupClient{}, fmt.Errorf("invoice trust configuration invalid")
	}
	return provider.SetupClient{Client: sdk, Verifier: verifier}, nil
}
func confirmed(r provider.SetupReceipt) bool { return r.SignatureVerified && r.Outcome == "confirmed" }
func developmentMatches(c setupConfig, service, merchant provider.SetupReceipt) bool {
	var a provider.DevelopmentConfig
	var b struct {
		Show *bool `json:"show_fapiao_cell"`
		VAT  *bool `json:"support_vat_fapiao"`
	}
	return confirmed(service) && confirmed(merchant) && json.Unmarshal(service.Body, &a) == nil && json.Unmarshal(merchant.Body, &b) == nil && a.CallbackURL == c.development().CallbackURL && b.Show != nil && b.VAT != nil && !*b.Show && !*b.VAT
}

// probe sends no credentials, refuses redirects, and has no business side effects.
func probe(ctx context.Context, address string) bool {
	r, e := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if e != nil {
		return false
	}
	cli := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := cli.Do(r)
	if e != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode == 200
}
func execute(ctx context.Context, c setupConfig, s provider.SetupClient, action string) receipt {
	out := receipt{Action: action, RequestedAt: time.Now().UTC()}
	switch action {
	case "read":
		out.Calls = []provider.SetupReceipt{s.Status(ctx, c.SubMerchantID), s.BaseInformation(ctx, c.SubMerchantID), s.TaxCodes(ctx, c.SubMerchantID), s.Development(ctx, ""), s.Development(ctx, c.SubMerchantID), s.InvitedMerchants(ctx, time.Now())}
		out.Complete = true
		for _, r := range out.Calls {
			out.Complete = out.Complete && confirmed(r)
		}
		out.IndependentReadback = out.Complete
	case "development":
		out.Request = c.development()
		if !probe(ctx, c.development().CallbackURL) {
			return out
		}
		update := s.ConfigureDevelopment(ctx, c.development())
		out.Calls = append(out.Calls, update)
		// Even an uncertain PATCH is read back before any further write. Never retry it.
		service, merchant := s.Development(ctx, ""), s.Development(ctx, c.SubMerchantID)
		out.Calls = append(out.Calls, service, merchant)
		out.IndependentReadback = developmentMatches(c, service, merchant)
		out.Complete = out.IndependentReadback
	case "card":
		card, valid := c.card()
		if !valid {
			return out
		}
		out.Request = card
		update := s.ConfigureCard(ctx, card)
		out.Calls = append(out.Calls, update)
		var result struct {
			AppID string `json:"card_appid"`
			ID    string `json:"card_id"`
		}
		out.Complete = confirmed(update) && json.Unmarshal(update.Body, &result) == nil && result.AppID == card.AppID && result.ID != ""
		// The official API exposes no card-template GET. This is a verified creation
		// receipt, not independent card-delivery evidence; do not claim otherwise.
	}
	return out
}

func summary(c setupConfig, r receipt) map[string]any {
	result := map[string]any{"complete": r.Complete, "action": r.Action, "independent_readback": r.IndependentReadback, "calls": len(r.Calls), "runtime_configuration_checked": false, "real_invoice_issued": false, "card_delivery_verified": false}
	if r.Action != "read" || len(r.Calls) != 6 {
		return result
	}
	var status struct {
		Mode struct {
			Status    string            `json:"status"`
			Expiry    time.Time         `json:"expired_time"`
			People    []json.RawMessage `json:"billing_person_info"`
			Abilities []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"ability_info"`
		} `json:"digital_tax_mode"`
	}
	if confirmed(r.Calls[0]) && json.Unmarshal(r.Calls[0].Body, &status) == nil {
		authorized := false
		for _, a := range status.Mode.Abilities {
			authorized = authorized || a.Type == "BASE_ABILITY" && a.Status == "AUTHORIZED"
		}
		result["merchant_authorized"] = status.Mode.Status == "ENABLED" && authorized && status.Mode.Expiry.After(time.Now())
		result["billing_person_count"] = len(status.Mode.People)
	}
	var taxes struct {
		Count int `json:"total_count"`
	}
	if confirmed(r.Calls[2]) && json.Unmarshal(r.Calls[2].Body, &taxes) == nil {
		result["tax_catalogue_count"] = taxes.Count
	}
	result["development_matches"] = developmentMatches(c, r.Calls[3], r.Calls[4])
	result["base_information_readable"] = confirmed(r.Calls[1])
	var invited struct {
		Merchants []struct {
			Sub   string `json:"sub_mchid"`
			State string `json:"mch_invite_status"`
			Name  string `json:"ep_name"`
			TaxID string `json:"tax_id"`
		} `json:"mch_invite_result_list"`
	}
	if confirmed(r.Calls[5]) && json.Unmarshal(r.Calls[5].Body, &invited) == nil {
		found := false
		for _, m := range invited.Merchants {
			found = found || m.Sub == c.SubMerchantID && m.State == "MCH_INVITE_SUCC" && m.Name != "" && m.TaxID != ""
		}
		result["seller_fact_available"] = found
	}
	return result
}
func main() {
	path := flag.String("config", "", "private setup JSON path")
	action := flag.String("action", "read", "read, development, or card")
	output := flag.String("receipt", "", "new private receipt file; never reuse after an unknown result")
	replace := flag.Bool("allow-card-replace", false, "explicitly authorize replacement of the merchant's official card template")
	flag.Parse()
	if *action != "read" && *action != "development" && *action != "card" || *output == "" || (*action == "card" && !*replace) {
		fmt.Println(`{"complete":false,"failure_stage":"arguments"}`)
		os.Exit(1)
	}
	c, e := readConfig(*path)
	if e != nil {
		fmt.Println(`{"complete":false,"failure_stage":"config"}`)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, e := client(ctx, c)
	if e != nil {
		fmt.Println(`{"complete":false,"failure_stage":"credentials"}`)
		os.Exit(1)
	}
	// Reserve a new receipt before an external write. An interrupted command leaves
	// a durable attempted marker, so rerunning the same action cannot overwrite it.
	f, e := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		fmt.Println(`{"complete":false,"failure_stage":"receipt"}`)
		os.Exit(1)
	}
	pending := receipt{Action: *action, RequestedAt: time.Now().UTC()}
	if *action == "development" {
		pending.Request = c.development()
	}
	if *action == "card" {
		pending.Request, _ = c.card()
	}
	if json.NewEncoder(f).Encode(pending) != nil || f.Sync() != nil {
		f.Close()
		fmt.Println(`{"complete":false,"failure_stage":"receipt"}`)
		os.Exit(1)
	}
	result := execute(ctx, c, s, *action)
	raw, _ := json.Marshal(result)
	// Append the final JSONL record; never remove the durable intent marker.
	if _, e = f.Write(append(raw, '\n')); e != nil || f.Sync() != nil {
		f.Close()
		os.Exit(1)
	}
	f.Close()
	// Only coarse state reaches stdout. Official billing names, taxpayer details
	// and response messages stay in the private receipt file.
	_ = json.NewEncoder(os.Stdout).Encode(summary(c, result))
	if !result.Complete {
		os.Exit(1)
	}
}
