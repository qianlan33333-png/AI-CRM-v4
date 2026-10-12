package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

// Uses the existing InstalledProduct process/Host binding and protected test
// database. This test creates no release runner and never installs an app.
func TestPostgreSQLProductImageDeliveryInstalledJourney(t *testing.T) {
	if testconfig.InstalledProduct().Binary == "" {
		t.Skip("requires the exact final InstalledProduct binary and Host")
	}
	if !platformconfig.ChromiumJourneyRequired() {
		t.Fatal("installed image acceptance requires AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	f, client, _ := periodicContactFixture(t)
	csrf := ""
	// Cookies belong to the real installed login performed by the existing fixture.
	requestURL, _ := http.NewRequest(http.MethodGet, f.server.URL, nil)
	for _, cookie := range client.Jar.Cookies(requestURL.URL) {
		if cookie.Name == "aicrm_admin_csrf" {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("installed login omitted CSRF")
	}
	type reply struct {
		status int
		body   []byte
		header http.Header
	}
	request := func(method, path, contentType, key string, body []byte, conditional string) reply {
		t.Helper()
		r, err := http.NewRequest(method, f.server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Accept", "application/json")
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		if method != http.MethodGet {
			r.Header.Set("X-CSRF-Token", csrf)
			r.Header.Set("Idempotency-Key", key)
		}
		if conditional != "" {
			r.Header.Set("If-None-Match", conditional)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatalf("installed %s %s failed: %T", method, path, err)
		}
		defer response.Body.Close()
		content, err := io.ReadAll(io.LimitReader(response.Body, 11<<20))
		if err != nil {
			t.Fatal(err)
		}
		return reply{response.StatusCode, content, response.Header}
	}
	upload := func(name, mime, key string, content []byte) reply {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename=%q`, name))
		header.Set("Content-Type", mime)
		part, err := form.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(content); err != nil {
			t.Fatal(err)
		}
		if err = form.Close(); err != nil {
			t.Fatal(err)
		}
		return request(http.MethodPost, "/api/admin/image-library/upload", form.FormDataContentType(), key, body.Bytes(), "")
	}
	narrow := imageDeliveryPNG(t, 1080, 2382)
	oversize := append(append([]byte{}, narrow...), make([]byte, mediaport.ManualImageUploadMaxBytes+1-len(narrow))...)
	counts := func() [3]int {
		var result [3]int
		if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM media_images),(SELECT count(*) FROM media_operation_receipts),(SELECT count(*) FROM media_blobs)`).Scan(&result[0], &result[1], &result[2]); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := counts()
	if response := upload("large.png", "image/png", "installed-image-too-large", oversize); response.status != 413 || !bytes.Contains(response.body, []byte("1MB")) {
		t.Fatalf("installed multipart image limit status=%d", response.status)
	}
	jsonBody, _ := json.Marshal(map[string]string{"file_name": "large.png", "data_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(oversize)})
	if response := request(http.MethodPost, "/api/admin/image-library", "application/json", "installed-image-json-too-large", jsonBody, ""); response.status != 413 {
		t.Fatalf("installed JSON image limit status=%d", response.status)
	}
	// A minimal local Excel plan allows the raw-cover rejection to pass through
	// the installed owner. No preparation component or Provider is required.
	var actor, planID int64
	if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT id FROM admin_users WHERE username='product-browser-owner'`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("installed-image-cover-plan"))
	if err := f.application.pool.Native().QueryRow(f.ctx, `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,target_count,pending_count,created_by) VALUES('image cover limit','excel_batch',$1,1,1,$2) RETURNING id`, digest[:], actor).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodPost, fmt.Sprintf("/api/admin/operation-batches/%d/cover?expected_version=1", planID), "image/png", "installed-image-cover-too-large", oversize, ""); response.status != 413 {
		t.Fatalf("installed Excel raw-cover limit status=%d", response.status)
	}
	if after := counts(); after != before {
		t.Fatalf("rejected installed image persisted facts: %v -> %v", before, after)
	}
	var version int64
	if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT version FROM ai_assistant_plans WHERE id=$1`, planID).Scan(&version); err != nil || version != 1 {
		t.Fatal("rejected cover mutated its plan")
	}

	type fixtureImage struct {
		name, mime string
		content    []byte
		id         int64
	}
	fixtures := []fixtureImage{
		{name: "narrow.png", mime: "image/png", content: narrow},
		{name: "wide.png", mime: "image/png", content: imageDeliveryPNG(t, 2160, 6000)},
		{name: "inflation.jpg", mime: "image/jpeg", content: imageDeliveryJPEG(t)},
	}
	var animation bytes.Buffer
	frame := image.NewPaletted(image.Rect(0, 0, 1200, 80), color.Palette{color.Black, color.White})
	if err := gif.EncodeAll(&animation, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{3, 7}, LoopCount: 2}); err != nil {
		t.Fatal(err)
	}
	fixtures = append(fixtures, fixtureImage{name: "animation.gif", mime: "image/gif", content: animation.Bytes()})
	for i := range fixtures {
		response := upload(fixtures[i].name, fixtures[i].mime, "installed-image-"+fixtures[i].name, fixtures[i].content)
		var result struct {
			ID int64 `json:"item_id"`
		}
		if response.status != 200 || json.Unmarshal(response.body, &result) != nil || result.ID < 1 {
			t.Fatalf("installed image accept %s status=%d", fixtures[i].name, response.status)
		}
		fixtures[i].id = result.ID
	}
	// Boundary upload and completed receipt replay use exactly the allowed bytes.
	exact := append(append([]byte{}, narrow...), make([]byte, mediaport.ManualImageUploadMaxBytes-len(narrow))...)
	first := upload("exact.png", "image/png", "installed-image-exact-boundary", exact)
	replay := upload("exact.png", "image/png", "installed-image-exact-boundary", exact)
	var exactFirst, exactReplay struct {
		ID int64 `json:"item_id"`
	}
	if first.status != 200 || replay.status != 200 || json.Unmarshal(first.body, &exactFirst) != nil || json.Unmarshal(replay.body, &exactReplay) != nil || exactFirst.ID < 1 || exactReplay.ID != exactFirst.ID {
		t.Fatal("installed exact-boundary receipt did not replay")
	}
	fixtures = append(fixtures, fixtureImage{name: "exact.png", mime: "image/png", content: exact, id: exactFirst.ID})
	// Seed a pre-policy completed original/receipt, then replay through the real
	// installed upload endpoint. Seeding never uses a bypass in runtime code.
	oldID := seedImageDeliveryOldReceipt(t, f, actor, oversize)
	oldReplay := upload("old.png", "image/png", "installed-old-image-completed", oversize)
	var old struct {
		ID int64 `json:"item_id"`
	}
	if oldReplay.status != 200 || json.Unmarshal(oldReplay.body, &old) != nil || old.ID != oldID {
		t.Fatalf("installed legacy receipt status=%d", oldReplay.status)
	}
	fixtures = append(fixtures, fixtureImage{name: "old.png", mime: "image/png", content: oversize, id: oldID})
	bindings := make([]string, 0, len(fixtures))
	for _, item := range fixtures {
		bindings = append(bindings, fmt.Sprintf("/api/admin/image-library/%d/variants/original", item.id))
	}
	bound, _ := json.Marshal(bindings)
	if _, err := f.application.pool.Native().Exec(f.ctx, `UPDATE products SET images=$1::jsonb WHERE id IN ($2,$3)`, bound, f.productID, f.serviceProductID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/p/browser-push-product", "/s/browser-push-service-period"} {
		page := request(http.MethodGet, path, "", "", nil, "")
		if page.status != 200 || !bytes.Contains(page.body, []byte("/product-public-assets/")) {
			t.Fatalf("installed customer page/Host did not load: %s status=%d", path, page.status)
		}
	}
	for index, item := range fixtures {
		originalPath := fmt.Sprintf("/api/admin/image-library/%d/variants/original", item.id)
		original := request(http.MethodGet, originalPath, "", "", nil, "")
		expectedETag := fmt.Sprintf(`"%x"`, sha256.Sum256(item.content))
		if original.status != 200 || !bytes.Equal(original.body, item.content) || original.header.Get("ETag") != expectedETag {
			t.Fatalf("installed original changed: %s status=%d", item.name, original.status)
		}
		path := fmt.Sprintf("/api/h5/product-images/browser-push-product/%d/variants/web_1080", item.id)
		web := request(http.MethodGet, path, "", "", nil, "")
		if web.status != 200 {
			t.Fatalf("installed ordinary variant %s status=%d", item.name, web.status)
		}
		if index == 1 {
			config, _, err := image.DecodeConfig(bytes.NewReader(web.body))
			if err != nil || config.Width != 1080 || config.Height != 3000 || len(web.body) >= len(item.content) {
				t.Fatal("installed beneficial wide image did not preserve proportion/reduce bytes")
			}
		} else if !bytes.Equal(web.body, item.content) || web.header.Get("ETag") != expectedETag {
			t.Fatalf("installed fallback/narrow image changed: %s", item.name)
		}
		if web.header.Get("ETag") == "" {
			t.Fatal("installed public variant omitted its ETag")
		}
		if conditional := request(http.MethodGet, originalPath, "", "", nil, expectedETag); conditional.status != 304 {
			t.Fatalf("installed Media conditional request status=%d", conditional.status)
		}
		periodPath := fmt.Sprintf("/api/h5/service-period-products/browser-push-service-period/images/%d/variants/original", item.id)
		period := request(http.MethodGet, periodPath, "", "", nil, "")
		if period.status != 200 || !bytes.Equal(period.body, item.content) {
			t.Fatalf("installed periodic original changed: %s status=%d", item.name, period.status)
		}
	}
	unbound := fmt.Sprintf("/api/h5/product-images/browser-push-product/%d/variants/web_1080", f.materialFirstID)
	if rejected := request(http.MethodGet, unbound, "", "", nil, ""); rejected.status != 404 {
		t.Fatalf("installed unbound public image status=%d", rejected.status)
	}
	// Reuse the existing Product Host journey with InstalledProduct active. It
	// opens both upload inputs, rejects >1 MiB, reselects, sorts and saves.
	TestPostgreSQLProductExternalPushChromiumJourney(t)
	t.Log("installed_product_image_delivery: PASS raw_originals widths smaller_only inflation animated_fallback upload_413 cover_413 completed_receipts binding_404 etag_304 ordinary_periodic_browser")
}

func imageDeliveryPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x / 20), G: uint8(y / 20), A: uint8(128 + x%128)})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func imageDeliveryJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1200, 2500))
	var v uint32 = 123456789
	for y := 0; y < 2500; y++ {
		for x := 0; x < 1200; x++ {
			v ^= v << 13
			v ^= v >> 17
			v ^= v << 5
			img.SetRGBA(x, y, color.RGBA{R: uint8(v), G: uint8(v >> 8), B: uint8(v >> 16), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 5}); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func seedImageDeliveryOldReceipt(t *testing.T, f *productExternalPushChromiumFixture, actor int64, content []byte) int64 {
	t.Helper()
	digestOf := func(value []byte) string {
		hash := sha256.Sum256(value)
		return "sha256:" + hex.EncodeToString(hash[:])
	}
	pool := f.application.pool.Native()
	blob := digestOf(content)
	if _, err := pool.Exec(f.ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'image/png',$2,$3) ON CONFLICT(digest) DO NOTHING`, blob, len(content), content); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := pool.QueryRow(f.ctx, `INSERT INTO media_images(blob_digest,file_name,name,mime_type,byte_size,width,height,enabled,created_by,updated_by) VALUES($1,'old.png','old.png','image/png',$2,1080,2382,true,$3,$3) RETURNING id`, blob, len(content), actor).Scan(&id); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(struct {
		F, M, N, D, T, C, H string
		S                   int
		W, X                int32
		E                   bool
	}{"old.png", "image/png", "old.png", "", "", "", blob, len(content), 1080, 2382, true})
	snapshot, _ := json.Marshal(map[string]any{"id": id, "file_name": "old.png", "name": "old.png", "mime_type": "image/png", "file_size": len(content), "width": 1080, "height": 2382, "enabled": true})
	if _, err := pool.Exec(f.ctx, `INSERT INTO media_operation_receipts(operation,actor_admin_user_id,resource_kind,resource_id,idempotency_key_digest,command_digest,result) VALUES('image.create',$1,'image',$2,$3,$4,$5)`, actor, id, digestOf([]byte("installed-old-image-completed")), digestOf(payload), snapshot); err != nil {
		t.Fatal(err)
	}
	return id
}
