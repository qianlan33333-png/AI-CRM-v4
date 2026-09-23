package donortemplate

import "testing"

func TestExtractPreservesCompleteNestedTemplateWorkspace(t *testing.T) {
	raw := `<!-- lead --><template id="tpl"><section data-note=">"><template data-sc-if="x"><button>保存</button></template><template data-sc-for="x">项目</template></section></template><p>outside</p>`
	got, err := Extract(raw)
	if err != nil || got != `<section data-note=">"><template data-sc-if="x"><button>保存</button></template><template data-sc-for="x">项目</template></section>` {
		t.Fatalf("Extract() = %q, %v", got, err)
	}
}
func TestExtractRejectsMissingAndIncompleteTemplate(t *testing.T) {
	if _, err := Extract(`<template data-sc-if="x"></template>`); err == nil || err.Error() != "donor template missing" {
		t.Fatalf("missing err=%v", err)
	}
	if _, err := Extract(`<template id="tpl"><template data-sc-if="x"></template>`); err == nil || err.Error() != "donor template incomplete" {
		t.Fatalf("incomplete err=%v", err)
	}
}
