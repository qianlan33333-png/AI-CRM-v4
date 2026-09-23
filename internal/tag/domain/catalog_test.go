package domain

import (
	"strings"
	"testing"
)

func TestValidateCatalogPreservesGroupAndTagOrderContract(t *testing.T) {
	valid := Catalog{
		Groups: []Group{
			{ID: 20, Name: "优先", SortOrder: 0},
			{ID: 10, Name: "次级", SortOrder: 1},
		},
		Tags: []Tag{
			{ID: 201, GroupID: 20, GroupName: "优先", Name: "A", SortOrder: 0},
			{ID: 202, GroupID: 20, GroupName: "优先", Name: "B", SortOrder: 1},
			{ID: 101, GroupID: 10, GroupName: "次级", Name: "C", SortOrder: 0},
		},
	}
	if err := ValidateCatalog(valid); err != nil {
		t.Fatalf("valid catalog rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Catalog){
		"group order":     func(catalog *Catalog) { catalog.Groups[0].SortOrder = 2 },
		"tag order":       func(catalog *Catalog) { catalog.Tags[1], catalog.Tags[2] = catalog.Tags[2], catalog.Tags[1] },
		"group mismatch":  func(catalog *Catalog) { catalog.Tags[0].GroupName = "错误" },
		"missing group":   func(catalog *Catalog) { catalog.Tags[0].GroupID = 99 },
		"duplicate group": func(catalog *Catalog) { catalog.Groups[1].ID = catalog.Groups[0].ID },
		"duplicate tag":   func(catalog *Catalog) { catalog.Tags[1].ID = catalog.Tags[0].ID },
		"invalid id":      func(catalog *Catalog) { catalog.Groups[0].ID = 0 },
		"negative order":  func(catalog *Catalog) { catalog.Tags[0].SortOrder = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneCatalog(valid)
			mutate(&candidate)
			if err := ValidateCatalog(candidate); err == nil {
				t.Fatalf("invalid catalog unexpectedly accepted: %#v", candidate)
			}
		})
	}
}

func TestValidateCatalogRejectsNilOrOverLimitCollections(t *testing.T) {
	if err := ValidateCatalog(Catalog{}); err == nil {
		t.Fatal("nil collections must fail closed")
	}
	groups := make([]Group, TagLimit+1)
	tags := make([]Tag, TagLimit+1)
	if err := ValidateCatalog(Catalog{Groups: groups, Tags: []Tag{}}); err == nil {
		t.Fatal("over-limit groups must fail closed")
	}
	if err := ValidateCatalog(Catalog{Groups: []Group{}, Tags: tags}); err == nil {
		t.Fatal("over-limit tags must fail closed")
	}
}

func TestValidTextAndCommandFieldsUseTrimmedUTF8RuneLimits(t *testing.T) {
	if !ValidText("中文标签") || ValidText(" 标签") || ValidText("标签 ") || ValidText("") || ValidText("\xff") {
		t.Fatal("text validation does not enforce trimmed valid UTF-8 text")
	}
	if ValidText(strings.Repeat("字", 201)) {
		t.Fatal("text validation must use the 200-rune limit")
	}
	valid := Command{Actor: 7, IdempotencyKey: "catalog-key-0001", TraceID: "trace-1", GroupName: "组"}
	if !ValidCommand(valid, "组", "标签") {
		t.Fatal("valid command rejected")
	}
	for name, mutate := range map[string]func(*Command){
		"missing actor": func(command *Command) { command.Actor = 0 },
		"short key":     func(command *Command) { command.IdempotencyKey = "short" },
		"padded key":    func(command *Command) { command.IdempotencyKey = " catalog-key-0001" },
		"padded trace":  func(command *Command) { command.TraceID = " trace-1" },
		"long trace":    func(command *Command) { command.TraceID = strings.Repeat("t", 201) },
		"invalid value": func(command *Command) { command.GroupName = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if ValidCommand(candidate, candidate.GroupName, "标签") {
				t.Fatalf("invalid command unexpectedly accepted: %#v", candidate)
			}
		})
	}
}

func TestReorderAndMutationResultIDsHaveDistinctContracts(t *testing.T) {
	if ValidIDs(nil) || ValidIDs([]int64{1, 1}) || ValidIDs([]int64{0}) || !ValidIDs([]int64{3, 2, 1}) {
		t.Fatal("reorder ID validation contract changed")
	}
	if !SameIDSet([]int64{1, 2, 3}, []int64{3, 1, 2}) || SameIDSet([]int64{1, 2}, []int64{1, 3}) || SameIDSet([]int64{1, 1}, []int64{1, 1}) {
		t.Fatal("reorder membership comparison contract changed")
	}
	if ValidResultIDs(nil) || ValidResultIDs([]int64{0}) || !ValidResultIDs([]int64{1, 1}) {
		t.Fatal("mutation result ID validation contract changed")
	}
	if !SameIDs([]int64{1, 2}, []int64{1, 2}) || SameIDs([]int64{1, 2}, []int64{2, 1}) {
		t.Fatal("ordered ID comparison contract changed")
	}
}

func cloneCatalog(catalog Catalog) Catalog {
	catalog.Groups = append([]Group(nil), catalog.Groups...)
	catalog.Tags = append([]Tag(nil), catalog.Tags...)
	return catalog
}
