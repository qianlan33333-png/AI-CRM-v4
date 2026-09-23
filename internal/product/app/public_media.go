package app

import (
	"net/url"
	"strconv"
	"strings"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// PublicProductImageIDs returns the image-library records explicitly attached
// to one currently enabled ordinary Product. The admin form persists selected
// image-library variants in Product.Images, so this is the Product-owned
// binding used by the anonymous reader; it never turns an arbitrary Media ID
// into a public resource.
func PublicProductImageIDs(product productport.Product) ([]int64, error) {
	local, err := ProjectLocalProduct(product)
	if err != nil || local.Lifecycle != productport.LocalProductEnabled || !local.Enabled || IsServicePeriodProjection(product.LegacyAdminProjection) {
		return nil, ErrNotFound
	}
	ids := make([]int64, 0, len(product.Images))
	seen := make(map[int64]struct{}, len(product.Images))
	for _, image := range product.Images {
		id, ok := productImageLibraryID(image)
		if !ok {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

// PublicProductImageURLs projects enabled Product image bindings into routes
// that an anonymous browser can load. The order is the persisted Product
// image order, so the selected primary image remains the card cover.
func PublicProductImageURLs(product productport.Product) ([]string, error) {
	ids, err := PublicProductImageIDs(product)
	if err != nil {
		return nil, err
	}
	allowed := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		allowed[id] = struct{}{}
	}
	urls := make([]string, 0, len(product.Images))
	for _, raw := range product.Images {
		if id, ok := productImageLibraryID(raw); ok {
			if _, present := allowed[id]; present {
				urls = append(urls, publicProductImageURL(product.ProductCode, id))
			}
			continue
		}
		if image, ok := publicHTTPSImage(raw); ok {
			urls = append(urls, image)
		}
	}
	return urls, nil
}

// publicProductCardCover uses the first Product-owned anonymous media URL.
func publicProductCardCover(product productport.Product) string {
	images, err := PublicProductImageURLs(product)
	if err != nil || len(images) == 0 {
		return ""
	}
	return images[0]
}

func publicProductImageURL(code string, imageID int64) string {
	return "/api/h5/product-images/" + url.PathEscape(code) + "/" + strconv.FormatInt(imageID, 10) + "/variants/original"
}

func productImageLibraryID(raw string) (int64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 2048 {
		return 0, false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return 0, false
	}
	const prefix = "/api/admin/image-library/"
	path := parsed.EscapedPath()
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 3 || parts[1] != "variants" || parts[2] != "original" {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != parts[0] {
		return 0, false
	}
	return id, true
}

func publicHTTPSImage(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 2048 {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.Scheme != "https" || parsed.Host == "" {
		return "", false
	}
	return parsed.String(), true
}
