package store

import (
	"context"
	"encoding/json"
	"fmt"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	d "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	"testing"
	"time"
)

func TestGroupRefreshTargetsOnlyActiveClosedRules(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	pool, e := pg.Wrap(native, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	u, e := pg.NewUnitOfWork(pool)
	if e != nil {
		t.Fatal(e)
	}
	r, e := NewPostgreSQL(native, u)
	if e != nil {
		t.Fatal(e)
	}
	at := time.Now().UTC()
	e = u.Within(ctx, func(c context.Context) error {
		g, e := r.CreateGroup(c, mustGroup(t, "g", at))
		if e != nil {
			return e
		}
		for i, active := range []bool{true, true, false} {
			p, e := r.CreatePackage(c, mustPackage(t, fmt.Sprintf("group-rule-%d", i), g.ID, at))
			if e != nil {
				return e
			}
			chat := "g"
			if !active {
				chat = "paused"
			}
			raw, _ := json.Marshal(map[string]any{"schema_version": 1, "template_key": "member_excluding_group_paid", "parameters": map[string]any{"exclude_group_chat": chat}})
			v, e := d.NewConfigurationVersion(p.ID, 1, raw, "", "daily_0200", 7, at)
			if e != nil {
				return e
			}
			v, e = r.CreateConfigurationVersion(c, v)
			if e != nil {
				return e
			}
			p, e = r.SetCurrentConfiguration(c, p.ID, v.ID, p.Version, 7, at)
			if e != nil {
				return e
			}
			if active {
				locked, e := r.LockPackage(c, p.ID)
				if e != nil {
					return e
				}
				if e = locked.Transition(d.Active, p.Version, 7, at); e != nil {
					return e
				}
				if _, e = r.UpdatePackage(c, locked, p.Version); e != nil {
					return e
				}
			}
		}
		out, e := r.ActiveGroupRefreshTargets(c)
		if e != nil {
			return e
		}
		if len(out) != 1 || out[0] != "g" {
			t.Fatalf("targets=%v", out)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
