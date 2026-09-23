package http

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"strings"
	"testing"

	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestPostgreSQLMaterialGroupManagementAtomicLifecycle(t *testing.T) {
	url, e := platformconfig.DatabaseURL()
	if e != nil {
		t.Skip("database URL not configured")
	}
	repo, cleanup, db := newHTTPIntegrationRepository(t, url)
	defer cleanup()
	service, e := mediaapp.NewHTTPFacade(repo)
	if e != nil {
		t.Fatal(e)
	}
	handler, e := NewHandler(service, handlerTestSecurity{})
	if e != nil {
		t.Fatal(e)
	}
	seq := 0
	call := func(method, path, body string, status int) map[string]any {
		seq++
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-CSRF-Token", "test-csrf")
		r.Header.Set("Idempotency-Key", fmt.Sprintf("group-lifecycle-%08d", seq))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return responseJSON(t, w, status)
	}
	ctx := context.Background()
	for _, kind := range []string{"image", "attachment", "miniprogram"} {
		t.Run(kind, func(t *testing.T) {
			base := "/api/admin/" + kind + "-library"
			created := call("POST", base+"/groups", `{"name":"课程 A&B"}`, 200)
			gid := int64(created["id"].(float64))
			call("POST", base+"/groups", `{"name":" 课程 A&B "}`, 409)
			call("POST", base+"/groups", `{"name":"未分组"}`, 400)
			groups := call("GET", base+"/groups", "", 200)
			found := false
			for _, raw := range groups["items"].([]any) {
				g := raw.(map[string]any)
				if g["id"] == float64(gid) && g["count"] == float64(0) {
					found = true
				}
			}
			if !found {
				t.Fatal("empty group lost")
			}

			var item map[string]any
			var err error
			switch kind {
			case "image":
				item, err = repo.CreateImage(ctx, 1, "groups-image-create-0001", mediaapp.ImageInput{FileName: "a.png", MIME: "image/png", Name: "a", Content: []byte("png"), Width: 1, Height: 1, GroupID: &gid})
			case "attachment":
				item, err = repo.CreateAttachment(ctx, 1, "groups-attachment-create-0001", mediaapp.AttachmentInput{FileName: "a.pdf", Name: "a", Content: []byte("%PDF-test"), GroupID: &gid})
			case "miniprogram":
				item, err = repo.CreateMiniProgram(ctx, 1, "groups-miniprogram-create-0001", map[string]any{"name": "a", "appid": "wx-test", "pagepath": "pages/a", "title": "a", "group_id": float64(gid)})
			}
			if err != nil {
				t.Fatal(err)
			}
			id := item["id"].(int64)
			// No dangling/mismatched membership or changing material identities on rename/delete.
			members, err := repo.MaterialGroupMembers(ctx, kind, []int64{id})
			if err != nil || len(members) != 1 || members[0]["category"] != "课程 A&B" {
				t.Fatalf("create group: %v %v", members, err)
			}
			table := map[string]string{"image": "media_images", "attachment": "media_attachments", "miniprogram": "media_miniprograms"}[kind]
			call("PUT", base+fmt.Sprintf("/groups/%d", gid), `{"name":"新课程","expected_version":1}`, 200)
			var category string
			var version int64
			if err = db.QueryRow(ctx, `SELECT category,version FROM `+table+` WHERE id=$1`, id).Scan(&category, &version); err != nil || category != "新课程" {
				t.Fatalf("rename %s %v", category, err)
			}
			// Batch second item conflicts: first item must stay in its original group.
			body := fmt.Sprintf(`{"group_id":null,"items":[{"id":%d,"expected_version":%d},{"id":99999999,"expected_version":1}]}`, id, version)
			call("POST", base+"/group-moves", body, 409)
			if err = db.QueryRow(ctx, `SELECT category FROM `+table+` WHERE id=$1`, id).Scan(&category); err != nil || category != "新课程" {
				t.Fatal("partial batch committed")
			}
			wrong, err := repo.ManageMaterialGroup(ctx, map[string]string{"image": "attachment", "attachment": "miniprogram", "miniprogram": "image"}[kind], "create", 1, "wrong-kind-group-"+kind, mediastore.GroupCommand{Name: "wrong"})
			if err != nil {
				t.Fatal(err)
			}
			wrongID := wrong["id"].(int64)
			call("POST", base+"/group-moves", fmt.Sprintf(`{"group_id":%d,"items":[{"id":%d,"expected_version":%d}]}`, wrongID, id, version), 404)
			call("DELETE", base+fmt.Sprintf("/groups/%d", gid), `{"expected_version":1}`, 409)
			call("DELETE", base+fmt.Sprintf("/groups/%d", gid), `{"expected_version":2}`, 200)
			var gotID *int64
			if err = db.QueryRow(ctx, `SELECT group_id,category FROM `+table+` WHERE id=$1`, id).Scan(&gotID, &category); err != nil || gotID != nil || category != "" {
				t.Fatalf("delete lost material or failed ungroup: %v %s %v", gotID, category, err)
			}
		})
	}
}

func TestPostgreSQLMaterialGroupReplayAndConcurrentDeleteMove(t *testing.T) {
	url, e := platformconfig.DatabaseURL()
	if e != nil {
		t.Skip("database URL not configured")
	}
	repo, cleanup, db := newHTTPIntegrationRepository(t, url)
	defer cleanup()
	ctx := context.Background()
	cmd := mediastore.GroupCommand{Name: "并发组"}
	first, e := repo.ManageMaterialGroup(ctx, "attachment", "create", 1, "concurrent-group-create", cmd)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := repo.ManageMaterialGroup(ctx, "attachment", "create", 1, "concurrent-group-create", cmd)
	if e != nil || fmt.Sprint(first["id"]) != fmt.Sprint(replay["id"]) {
		t.Fatal("create replay changed identity")
	}
	gid := first["id"].(int64)
	item, e := repo.CreateAttachment(ctx, 1, "concurrent-group-pdf-create", mediaapp.AttachmentInput{FileName: "a.pdf", Name: "a", Content: []byte("%PDF-test")})
	if e != nil {
		t.Fatal(e)
	}
	id := item["id"].(int64)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, e := repo.ManageMaterialGroup(ctx, "attachment", "delete", 1, "concurrent-group-delete", mediastore.GroupCommand{ID: gid, Version: 1})
		results <- e
	}()
	go func() {
		<-start
		_, e := repo.ManageMaterialGroup(ctx, "attachment", "move", 1, "concurrent-group-move", mediastore.GroupCommand{GroupID: &gid, Items: []mediastore.GroupMember{{ID: id, Version: 1}}})
		results <- e
	}()
	close(start)
	for i := 0; i < 2; i++ {
		e = <-results
		if e != nil && !errors.Is(e, mediastore.ErrConflict) && !errors.Is(e, mediastore.ErrNotFound) {
			t.Fatal(e)
		}
	}
	var missing int
	if e = db.QueryRow(ctx, `SELECT count(*) FROM media_attachments m LEFT JOIN media_material_groups g ON g.id=m.group_id WHERE m.group_id IS NOT NULL AND g.id IS NULL`).Scan(&missing); e != nil || missing != 0 {
		t.Fatalf("dangling membership %d %v", missing, e)
	}
	var groupCount int
	if e = db.QueryRow(ctx, `SELECT count(*) FROM media_material_groups WHERE id=$1`, gid).Scan(&groupCount); e != nil || groupCount != 0 {
		t.Fatal("delete not committed", e)
	}
	var materialCount int
	if e = db.QueryRow(ctx, `SELECT count(*) FROM media_attachments WHERE id=$1 AND group_id IS NULL AND category=''`, id).Scan(&materialCount); e != nil || materialCount != 1 {
		t.Fatal("material lost", e)
	}
}

func TestPostgreSQLMaterialGroupMigrationPreservesLegacyCategories(t *testing.T) {
	url, e := platformconfig.DatabaseURL()
	if e != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	_, cleanup, db := newHTTPIntegrationRepository(t, url, func(db *pgxpool.Pool) {
		_, e := db.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES('sha256:'||repeat('a',64),'image/png',1,'x');
   INSERT INTO media_images(blob_digest,file_name,name,category,mime_type,byte_size,width,height,created_by,updated_by) VALUES('sha256:'||repeat('a',64),'old.png','old',repeat('中',140),'image/png',1,1,1,1,1);
   INSERT INTO media_attachments(blob_digest,file_name,name,category,mime_type,byte_size,created_by,updated_by) VALUES('sha256:'||repeat('a',64),'old.pdf','old','课程 A&B','application/pdf',1,1,1);
   INSERT INTO media_miniprograms(name,app_id,page_path,title,category,created_by,updated_by) VALUES('old','wxlegacy','pages/a','old','课程 A&B',1,1);`)
		if e != nil {
			t.Fatal(e)
		}
	})
	defer cleanup()
	for _, pair := range [][2]string{{"media_images", "image"}, {"media_attachments", "attachment"}, {"media_miniprograms", "miniprogram"}} {
		var count int
		e = db.QueryRow(ctx, `SELECT count(*) FROM `+pair[0]+` m JOIN media_material_groups g ON g.id=m.group_id WHERE m.category=g.name AND g.kind=$1`, pair[1]).Scan(&count)
		if e != nil || count != 1 {
			t.Fatalf("legacy membership %s: %d %v", pair[1], count, e)
		}
	}
	var name string
	if e = db.QueryRow(ctx, `SELECT category FROM media_images`).Scan(&name); e != nil || name != strings.Repeat("中", 140) {
		t.Fatal("legacy category truncated", e)
	}
}
