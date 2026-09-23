// Package address owns the versioned mainland China province/city/district
// catalog used by checkout presentation and server-side validation.
package port

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

//go:embed data/pca.json
var catalogJSON []byte

type node struct {
	Code     string `json:"-"`
	Name     string `json:"n"`
	Children []node `json:"ch,omitempty"`
}

type rawNode struct {
	Code     json.RawMessage `json:"c"`
	Name     string          `json:"n"`
	Children []rawNode       `json:"ch"`
}

var catalog = loadCatalog()

func loadCatalog() []node {
	var raw []rawNode
	if json.Unmarshal(catalogJSON, &raw) != nil {
		return nil
	}
	result := make([]node, 0, len(raw))
	for _, item := range raw {
		result = append(result, normalize(item))
	}
	return result
}

func normalize(item rawNode) node {
	code := strings.Trim(string(item.Code), `"`)
	children := make([]node, 0, len(item.Children))
	for _, child := range item.Children {
		children = append(children, normalize(child))
	}
	return node{Code: code, Name: item.Name, Children: children}
}

// OptionsJSON returns the canonical cascade options as an immutable JSON copy.
func OptionsJSON() []byte { return append([]byte(nil), catalogJSON...) }

// Validate verifies the exact province -> city -> district lineage and names.
func Validate(provinceCode, provinceName, cityCode, cityName, districtCode, districtName string) error {
	if strings.TrimSpace(provinceCode) == "" || strings.TrimSpace(cityCode) == "" || strings.TrimSpace(districtCode) == "" || provinceName == "" || cityName == "" || districtName == "" {
		return errors.New("address fields are required")
	}
	for _, province := range catalog {
		if province.Code != provinceCode || province.Name != provinceName {
			continue
		}
		for _, city := range province.Children {
			if city.Code != cityCode || city.Name != cityName {
				continue
			}
			for _, district := range city.Children {
				if district.Code == districtCode && district.Name == districtName {
					return nil
				}
			}
		}
	}
	return errors.New("invalid address hierarchy")
}

// ValidateCodes returns the canonical names for a valid lineage.
func ValidateCodes(provinceCode, cityCode, districtCode string) (provinceName, cityName, districtName string, err error) {
	for _, province := range catalog {
		if province.Code != provinceCode {
			continue
		}
		for _, city := range province.Children {
			if city.Code != cityCode {
				continue
			}
			for _, district := range city.Children {
				if district.Code == districtCode {
					return province.Name, city.Name, district.Name, nil
				}
			}
		}
	}
	return "", "", "", fmt.Errorf("invalid address hierarchy")
}

// IsAvailable is used by readiness tests to fail closed if the embedded
// catalog is accidentally omitted from a release.
func IsAvailable() bool { return len(bytes.TrimSpace(catalogJSON)) > 2 && len(catalog) > 0 }
