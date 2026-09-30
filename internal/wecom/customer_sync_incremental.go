package wecom

import (
	"context"
	"fmt"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"time"
)

// PublishNewContactWithin is called only by the authenticated add-contact detail
// worker after its Provider read. It shares the same publisher and tag history.
func (s CustomerSyncService) PublishNewContactWithin(ctx context.Context, inboxID int64, contact wecomport.ExternalContact, observedAt time.Time) error {
	if inboxID < 1 || s.IdentityResolver == nil {
		return ErrSyncNotReady
	}
	result, err := s.IdentityResolver.Resolve(ctx, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:" + s.CorpID, Value: contact.ExternalUserID, Assurance: identitydomain.AssuranceVerified, Source: "wecom.callback_detail"})
	if err != nil {
		return err
	}
	if result.Status != identityport.ResolveFound || result.CustomerID < 1 || result.IdentityID < 1 {
		return nil
	}
	store, ok := s.Store.(directoryPublicationStore)
	if !ok {
		return ErrSyncNotReady
	}
	run, replay, err := s.Store.Create(ctx, CreateCustomerSyncRun{RunKey: fmt.Sprintf("wecom-new-contact:%d", inboxID), Trigger: "new_contact", CorpScope: "wecom-corp:" + s.CorpID})
	if err != nil {
		return err
	}
	if replay && run.Status == SyncSucceeded {
		return nil
	}
	if run.Status == SyncQueued {
		if err = s.Store.Transition(ctx, run.ID, run.Version, SyncQueued, SyncReconciling); err != nil {
			return err
		}
		run, err = s.Store.Get(ctx, run.ID)
		if err != nil {
			return err
		}
	}
	if err = store.StageContact(ctx, run.ID, identityport.ProvisionResult{CustomerID: result.CustomerID, IdentityID: result.IdentityID}, contact, observedAt); err != nil {
		return err
	}
	if err = s.publishStaged(ctx, run, s.now()); err != nil {
		return err
	}
	if reconciler, ok := s.Store.(interface {
		ReconcileCustomerFollows(context.Context, int64, int64, time.Time) error
	}); ok {
		if err = reconciler.ReconcileCustomerFollows(ctx, run.ID, int64(result.CustomerID), observedAt); err != nil {
			return err
		}
	}
	if err = s.Store.RefreshProfilePrimaryOwners(ctx, run.ID, s.now()); err != nil {
		return err
	}
	if err = s.Store.Complete(ctx, run.ID, run.Version, 0); err != nil {
		return err
	}
	if err = store.EndPublication(ctx, run, s.now()); err != nil {
		return err
	}
	return s.notifyPublication(ctx, run.CorpScope)
}
