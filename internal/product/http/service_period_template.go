package http

import (
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// servicePeriodPublicState is the small Host-owned fact set substituted into
// the frozen renderer. Product supplies already-public product facts; Payment
// supplies the trusted session and Order supplies an entitlement projection.
// No donor business code is executed.
type servicePeriodPublicState struct {
	Available                              bool
	LeadQRURL, LeadQRTitle, LeadQRSubtitle string
	Product                                publicProduct
	Status                                 string
	CTA                                    string
	EndAt                                  time.Time
	RemainingDays                          int32
}

// renderServicePeriodPublicPage adapts the exact HTML f-string returned by
// the frozen donor's render_service_period_public_page. Keeping the source
// body as the rendering template preserves its DOM, styles and state script;
// this function substitutes only the V3 Host facts that the Python code used
// to calculate before rendering.
func renderServicePeriodPublicPage(w io.Writer, state servicePeriodPublicState) error {
	return renderServicePeriodPublicPageWithPresentation(w, state, PublicPresentationAssets{})
}

func renderServicePeriodPublicPageWithPresentation(w io.Writer, state servicePeriodPublicState, presentation PublicPresentationAssets) error {
	page := frozenServicePeriodPageBody()
	if page == "" {
		return fmt.Errorf("frozen service-period renderer body unavailable")
	}
	// This is an explicitly bounded Host adaptation to the frozen static
	// script. The server already renders dates in Asia/Shanghai; use the same
	// zone after the script refreshes its state so an evening UTC expiry cannot
	// display two different calendar dates on one page.
	page = strings.Replace(page, frozenServicePeriodEndDateFunction, servicePeriodShanghaiEndDateFunction, 1)

	status := state.Status
	if status == "" {
		status = "none"
	}
	price := fmt.Sprintf("%.2f", float64(state.Product.PriceMinor)/100)
	title := html.EscapeString(state.Product.Name)
	cta := html.EscapeString(state.CTA)
	if cta == "" {
		cta = "立即报名"
	}

	var tagText, heroText, barMeta, card string
	tagHidden, heroHidden, wecomHidden := " hidden", " hidden", " hidden"
	switch {
	case !state.Available || status == "unavailable":
		status = "unavailable"
		tagText, heroText, barMeta = "未上架", "该周期商品暂未开放", "暂未开放"
		tagHidden, heroHidden = "", ""
		if state.CTA == "" {
			cta = "暂未开放"
		}
		card = servicePeriodUnavailableCard(price, state.Product.ServicePeriodDurationDays)
	case status == "active":
		tagText, barMeta = "服务中", fmt.Sprintf("剩余 %d 天", state.RemainingDays)
		tagHidden = ""
		pct := 0
		if state.Product.ServicePeriodDurationDays > 0 {
			pct = int((int64(state.RemainingDays)*100 + int64(state.Product.ServicePeriodDurationDays)/2) / int64(state.Product.ServicePeriodDurationDays))
		}
		if pct > 100 {
			pct = 100
		}
		card = servicePeriodActiveCard(state.RemainingDays, pct, state.EndAt, price, state.Product.ServicePeriodDurationDays)
		if state.LeadQRURL != "" {
			wecomHidden = ""
		}
	case status == "expired", status == "refunded":
		status, tagText, heroText = "expired", "已过期", "服务期已结束"
		tagHidden, heroHidden = "", ""
		barMeta = fmt.Sprintf("¥%s / %d 天", price, state.Product.ServicePeriodDurationDays)
		card = servicePeriodExpiredCard(state.EndAt, price, state.Product.ServicePeriodDurationDays)
	default:
		status, barMeta = "none", fmt.Sprintf("¥%s / %d 天", price, state.Product.ServicePeriodDurationDays)
		card = servicePeriodNoneCard(price, state.Product.ServicePeriodDurationDays)
	}

	// Decode only static Python f-string segments before inserting trusted Host
	// facts. The frozen source contains both doubled f-string braces and Python
	// escapes (notably `\\\\` in JavaScript regexes). A whole-page replacement
	// after substitution would also rewrite legitimate braces/backslashes in a
	// product title, QR text, or JSON value.
	page, err := renderFrozenPythonFString(page, []frozenFStringReplacement{
		{expression: "{title}", value: title},
		{expression: "{escape(status)}", value: html.EscapeString(status)},
		{expression: "{tag_hidden}", value: tagHidden},
		{expression: "{escape(tag_text)}", value: html.EscapeString(tagText)},
		{expression: "{hero_text_hidden}", value: heroHidden},
		{expression: "{escape(hero_text)}", value: html.EscapeString(heroText)},
		{expression: "{card_html}", value: card},
		{expression: "{wecom_action_hidden}", value: wecomHidden},
		{expression: "{media}", value: servicePeriodDetailMedia(state.Product.Images)},
		{expression: "{escape(bar_meta)}", value: html.EscapeString(barMeta)},
		{expression: "{cta_text}", value: cta},
		{expression: "{render_lead_qr_modal()}", value: servicePeriodLeadQRModal()},
		{expression: "{lead_qr_modal_controller_script()}", value: servicePeriodLeadQRController()},
		{expression: "{lead_qr_modal_styles()}", value: servicePeriodLeadQRStyles()},
		{expression: "{state_json}", value: servicePeriodStateJSON(state, status)},
		{expression: "{duration_days}", value: strconv.FormatInt(int64(state.Product.ServicePeriodDurationDays), 10)},
		{expression: "{price_yuan}", value: price},
		// V2 exchanged a fragment credential. V3 deliberately omits it: the
		// existing Payment H5 OAuth callback issues the only trusted HttpOnly
		// session that this public page subsequently reads server-side.
		{expression: "{product_context_fragment_bootstrap_script()}", value: ""},
	})
	if err != nil {
		return err
	}
	if presentation.configured() {
		page, err = decorateFrozenServicePeriodPresentation(page, presentation)
		if err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, page)
	return err
}

func decorateFrozenServicePeriodPresentation(page string, presentation PublicPresentationAssets) (string, error) {
	if !presentation.configured() {
		return page, nil
	}
	const closingHead = "</head>"
	const servicePeriodRoot = "<main class=\"service-period-page "
	if strings.Count(page, closingHead) != 1 || strings.Count(page, servicePeriodRoot) != 1 {
		return "", fmt.Errorf("frozen service-period page has no stable presentation insertion point")
	}
	assets := `<link rel="stylesheet" href="` + html.EscapeString(presentation.StylesheetURL) + `"><script type="module" src="` + html.EscapeString(presentation.HostURL) + `"></script>`
	page = strings.Replace(page, closingHead, assets+closingHead, 1)
	return strings.Replace(page, servicePeriodRoot, `<main data-v3-public-commerce data-public-commerce-route="service-period-state" data-product-kind="service_period" class="service-period-page "`, 1), nil
}

const frozenServicePeriodEndDateFunction = `      function endDate(value) {{
        return value ? String(value).slice(0, 10) : "-";
      }`

const servicePeriodShanghaiEndDateFunction = `      function endDate(value) {{
        if (!value) return "-";
        const parsed = new Date(value);
        if (Number.isNaN(parsed.getTime())) return "-";
        const parts = new Intl.DateTimeFormat("zh-CN", {{
          timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit"
        }}).formatToParts(parsed);
        const pick = function (type) {{
          const item = parts.find(function (part) {{ return part.type === type; }});
          return item ? item.value : "";
        }};
        return pick("year") + "-" + pick("month") + "-" + pick("day");
      }`

type frozenFStringReplacement struct {
	expression string
	value      string
}

// renderFrozenPythonFString implements the small, explicit subset of the
// frozen f-string used by the donor. Expressions are enumerated by the Host;
// all text between them is decoded as a Python triple-quoted f-string literal.
// It intentionally has no evaluator for donor expressions or user content.
func renderFrozenPythonFString(source string, replacements []frozenFStringReplacement) (string, error) {
	var out strings.Builder
	staticStart := 0
	for index := 0; index < len(source); {
		matched := frozenFStringReplacement{}
		for _, candidate := range replacements {
			if strings.HasPrefix(source[index:], candidate.expression) {
				matched = candidate
				break
			}
		}
		if matched.expression == "" {
			_, size := utf8.DecodeRuneInString(source[index:])
			index += size
			continue
		}
		literal, err := decodeFrozenPythonLiteral(source[staticStart:index], true)
		if err != nil {
			return "", err
		}
		out.WriteString(literal)
		out.WriteString(matched.value)
		index += len(matched.expression)
		staticStart = index
	}
	literal, err := decodeFrozenPythonLiteral(source[staticStart:], true)
	if err != nil {
		return "", err
	}
	out.WriteString(literal)
	return out.String(), nil
}

// decodeFrozenPythonLiteral keeps only Python's static literal rules. Unknown
// escapes are retained as Python does for a non-raw literal, instead of being
// interpreted by Go or JavaScript.
func decodeFrozenPythonLiteral(source string, fString bool) (string, error) {
	var out strings.Builder
	for index := 0; index < len(source); {
		switch source[index] {
		case '{':
			if fString && index+1 < len(source) && source[index+1] == '{' {
				out.WriteByte('{')
				index += 2
				continue
			}
		case '}':
			if fString && index+1 < len(source) && source[index+1] == '}' {
				out.WriteByte('}')
				index += 2
				continue
			}
		case '\\':
			if index+1 == len(source) {
				return "", fmt.Errorf("frozen Python literal ends with an escape")
			}
			next := source[index+1]
			switch next {
			case '\\', '\'', '"':
				out.WriteByte(next)
				index += 2
				continue
			case 'a':
				out.WriteByte('\a')
			case 'b':
				out.WriteByte('\b')
			case 'f':
				out.WriteByte('\f')
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			case 'v':
				out.WriteByte('\v')
			case '\n':
				index += 2
				continue
			case 'x', 'u', 'U':
				digits := map[byte]int{'x': 2, 'u': 4, 'U': 8}[next]
				if index+2+digits > len(source) {
					return "", fmt.Errorf("invalid frozen Python %c escape", next)
				}
				value, err := strconv.ParseUint(source[index+2:index+2+digits], 16, 32)
				if err != nil || value > utf8.MaxRune || (value >= 0xD800 && value <= 0xDFFF) {
					return "", fmt.Errorf("invalid frozen Python %c escape", next)
				}
				out.WriteRune(rune(value))
				index += 2 + digits
				continue
			default:
				if next >= '0' && next <= '7' {
					end := index + 2
					for end < len(source) && end < index+4 && source[end] >= '0' && source[end] <= '7' {
						end++
					}
					value, err := strconv.ParseUint(source[index+1:end], 8, 8)
					if err != nil {
						return "", fmt.Errorf("invalid frozen Python octal escape")
					}
					out.WriteByte(byte(value))
					index = end
					continue
				}
				// Python preserves an unrecognised escape (and emits a warning).
				out.WriteByte('\\')
				out.WriteByte(next)
				index += 2
				continue
			}
			index += 2
			continue
		}
		_, size := utf8.DecodeRuneInString(source[index:])
		out.WriteString(source[index : index+size])
		index += size
	}
	return out.String(), nil
}

func frozenServicePeriodPageBody() string {
	const start = "return f\"\"\"<!doctype html>"
	from := strings.Index(frozenServicePeriodPublicRenderer, start)
	if from < 0 {
		return ""
	}
	from += len("return f\"\"\"")
	to := strings.Index(frozenServicePeriodPublicRenderer[from:], "\"\"\"\n\n\ndef render_service_period_pay_page")
	if to < 0 {
		return ""
	}
	return frozenServicePeriodPublicRenderer[from : from+to]
}

func servicePeriodNoneCard(price string, duration int32) string {
	return fmt.Sprintf("\n      <div class=\"service-period-price\"><small>¥</small>%s</div>\n      <div class=\"service-period-line\"><span class=\"service-period-muted\">有效期</span><strong>%d 天</strong></div>", price, duration)
}

func servicePeriodUnavailableCard(price string, duration int32) string {
	return servicePeriodNoneCard(price, duration) + "\n      <p class=\"service-period-tip\">该周期商品尚未上架，暂不可购买。</p>"
}

func servicePeriodActiveCard(days int32, percent int, end time.Time, price string, duration int32) string {
	return fmt.Sprintf("\n      <div class=\"service-period-muted\">剩余有效期</div>\n      <div class=\"service-period-state-big\">%d 天</div>\n      <div class=\"service-period-progress\" aria-label=\"剩余服务期\"><span style=\"width:%d%%\"></span></div>\n      <div class=\"service-period-line\"><span class=\"service-period-muted\">到期日</span><strong>%s</strong></div>\n      <div class=\"service-period-line\"><span class=\"service-period-muted\">续费价格 / 有效期</span><strong>¥%s / %d 天</strong></div>", days, percent, servicePeriodEndDate(end), price, duration)
}

func servicePeriodExpiredCard(end time.Time, price string, duration int32) string {
	return fmt.Sprintf("\n      <div class=\"service-period-state-big\">已过期</div>\n      <div class=\"service-period-line\"><span class=\"service-period-muted\">上次到期日</span><strong>%s</strong></div>\n      <div class=\"service-period-line\"><span class=\"service-period-muted\">重新开通价格 / 有效期</span><strong>¥%s / %d 天</strong></div>", servicePeriodEndDate(end), price, duration)
}

func servicePeriodEndDate(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
}

func servicePeriodDetailMedia(images []string) string {
	if len(images) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("    <section class=\"detail-media\" aria-label=\"商品详情图\">\n")
	for index, image := range images {
		if image == "" {
			continue
		}
		loading, priority := "lazy", ""
		if index == 0 {
			loading, priority = "eager", " fetchpriority=\"high\""
		}
		fmt.Fprintf(&b, "      <img class=\"slice-img\" src=\"%s\" loading=\"%s\" decoding=\"async\"%s alt=\"\">\n", html.EscapeString(image), loading, priority)
	}
	b.WriteString("    </section>")
	return b.String()
}
