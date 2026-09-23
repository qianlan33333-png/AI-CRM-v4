package channel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

var ErrAcquisitionLinkUnavailable = errors.New("customer acquisition link unavailable")

type AcquisitionLinkReceipt struct {
	ID                                                                      int64
	Operation, RequestedLinkID, State, EffectRef, OutcomeDigest, Resolution string
	Link                                                                    *wecomport.CustomerAcquisitionLink
	BusinessEndpointDispatched, RealExternalCallExecuted                    bool
}
type AcquisitionLinkCommand struct {
	ActorID                           int64
	IdempotencyKey, Operation, LinkID string
	Input                             wecomport.CustomerAcquisitionLinkInput
}

type AcquisitionLinkReconcileCommand struct {
	ActorID        int64
	ReceiptID      int64
	LinkID         string
	Resolution     string
	EvidenceDigest string
	IdempotencyKey string
}

type AcquisitionLinkReconciler interface {
	ReconcileAcquisitionLink(context.Context, AcquisitionLinkReconcileCommand) (AcquisitionLinkReceipt, error)
}
type AcquisitionLinkStore struct{}

func NewAcquisitionLinkStore() *AcquisitionLinkStore { return &AcquisitionLinkStore{} }

type AcquisitionLinkService struct {
	uow        platformport.UnitOfWork
	store      *AcquisitionLinkStore
	effects    effectport.TransactionalAccepter
	reconciler AcquisitionLinkReconciler
	provider   wecomport.CustomerAcquisitionLinkProvider
}

func (service *AcquisitionLinkService) SetProvider(provider wecomport.CustomerAcquisitionLinkProvider) error {
	if service == nil || provider == nil || service.provider != nil {
		return ErrAcquisitionLinkUnavailable
	}
	service.provider = provider
	return nil
}

func NewAcquisitionLinkService(uow platformport.UnitOfWork, store *AcquisitionLinkStore, effects effectport.TransactionalAccepter) *AcquisitionLinkService {
	return &AcquisitionLinkService{uow: uow, store: store, effects: effects}
}

func (service *AcquisitionLinkService) SetReconciler(reconciler AcquisitionLinkReconciler) error {
	if service == nil || reconciler == nil || service.reconciler != nil {
		return ErrAcquisitionLinkUnavailable
	}
	service.reconciler = reconciler
	return nil
}

func (service *AcquisitionLinkService) Reconcile(ctx context.Context, command AcquisitionLinkReconcileCommand) (AcquisitionLinkReceipt, error) {
	if service == nil || service.reconciler == nil || command.ActorID < 1 || command.ReceiptID < 1 || !validLinkID(command.LinkID) || !validOperationKey(command.IdempotencyKey) || !effectport.ValidDigest(effectport.Digest(command.EvidenceDigest)) || (command.Resolution != "provider_applied" && command.Resolution != "provider_not_applied") {
		return AcquisitionLinkReceipt{}, ErrInvalidCatalogCommand
	}
	return service.reconciler.ReconcileAcquisitionLink(ctx, command)
}
func (service *AcquisitionLinkService) Mutate(ctx context.Context, command AcquisitionLinkCommand) (AcquisitionLinkReceipt, error) {
	if service == nil || service.uow == nil || service.store == nil || service.effects == nil || service.provider == nil || !validLinkCommand(command) {
		return AcquisitionLinkReceipt{}, ErrInvalidCatalogCommand
	}
	requestJSON, _ := json.Marshal(command)
	requestDigest := sha256.Sum256(requestJSON)
	key := sha256.Sum256([]byte(command.IdempotencyKey))
	var replay AcquisitionLinkReceipt
	var replayed bool
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		existing, digest, found, err := service.store.byOperation(tx, command.ActorID, key)
		if err != nil {
			return err
		}
		if found && digest != requestDigest {
			return ErrCatalogConflict
		}
		replay, replayed = existing, found
		return nil
	}); err != nil {
		return AcquisitionLinkReceipt{}, err
	}
	if replayed {
		return replay, nil
	}
	// Provider reads are fail-closed preconditions and must never hold the local
	// business transaction. The accepted write below remains purely local.
	if command.Operation != "create" {
		if _, err := service.provider.GetManagedAcquisitionLink(ctx, command.LinkID); err != nil {
			return AcquisitionLinkReceipt{}, err
		}
	}
	var result AcquisitionLinkReceipt
	err := service.uow.Within(ctx, func(tx context.Context) error {
		existing, digest, found, err := service.store.byOperation(tx, command.ActorID, key)
		if err != nil {
			return err
		}
		if found {
			if digest != requestDigest {
				return ErrCatalogConflict
			}
			result = existing
			return nil
		}
		source := effectport.Hash("channel.link.mutation.source.v1", strconv.FormatInt(command.ActorID, 10), command.IdempotencyKey)
		envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindChannelLink, SourceRefDigest: source, TargetRefDigest: effectport.Hash("channel.link.mutation.target.v1", command.LinkID), PayloadDigest: effectport.Hash("channel.link.mutation.payload.v1", string(requestJSON)), PolicyVersionHash: effectport.Hash("channel.link.mutation.policy", "v1")}
		projection, receipt, err := service.effects.AcceptAndQueueWithin(tx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("channel.link.mutation.accept.v1", strconv.FormatInt(command.ActorID, 10), command.IdempotencyKey), Envelope: envelope})
		if err != nil {
			return err
		}
		result = AcquisitionLinkReceipt{Operation: command.Operation, RequestedLinkID: command.LinkID, State: "accepted", EffectRef: projection.ID}
		result, err = service.store.insert(tx, result, command, key, requestDigest, string(source), receipt.ID, receipt.QueueReceiptID)
		return err
	})
	return result, err
}
func (service *AcquisitionLinkService) List(ctx context.Context, cursor string, limit int) ([]wecomport.CustomerAcquisitionLink, string, error) {
	if service == nil || service.provider == nil || limit < 1 || limit > 100 || strings.TrimSpace(cursor) != cursor || len(cursor) > 1024 {
		return nil, "", ErrInvalidCatalogCommand
	}
	page, err := service.provider.ListManagedAcquisitionLinks(ctx, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	if len(page.Links) > limit || strings.TrimSpace(page.NextCursor) != page.NextCursor || len(page.NextCursor) > 1024 {
		return nil, "", ErrAcquisitionLinkUnavailable
	}
	return page.Links, page.NextCursor, nil
}
func (service *AcquisitionLinkService) Get(ctx context.Context, id string) (wecomport.CustomerAcquisitionLink, error) {
	var result wecomport.CustomerAcquisitionLink
	if service == nil || service.provider == nil || !validLinkID(id) {
		return result, ErrInvalidCatalogCommand
	}
	return service.provider.GetManagedAcquisitionLink(ctx, id)
}

func (store *AcquisitionLinkStore) byOperation(ctx context.Context, actor int64, key [32]byte) (AcquisitionLinkReceipt, [32]byte, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return AcquisitionLinkReceipt{}, [32]byte{}, false, err
	}
	var result AcquisitionLinkReceipt
	var id int64
	var digest []byte
	err = tx.QueryRow(ctx, `SELECT id,request_digest FROM channel_acquisition_link_receipts WHERE actor_admin_user_id=$1 AND operation_key_digest=$2`, actor, key[:]).Scan(&id, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, [32]byte{}, false, nil
	}
	var value [32]byte
	if len(digest) == 32 {
		copy(value[:], digest)
	}
	if err != nil {
		return result, value, false, err
	}
	result, err = store.receiptByID(ctx, id)
	return result, value, err == nil, err
}
func (store *AcquisitionLinkStore) insert(ctx context.Context, result AcquisitionLinkReceipt, command AcquisitionLinkCommand, key, request [32]byte, source, accept, queue string) (AcquisitionLinkReceipt, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return result, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO channel_acquisition_link_receipts(actor_admin_user_id,operation_key_digest,request_digest,operation,requested_link_id,link_name,user_ids,department_ids,skip_verify,source_ref_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'accepted') RETURNING id`, command.ActorID, key[:], request[:], command.Operation, command.LinkID, command.Input.LinkName, command.Input.UserIDs, command.Input.DepartmentIDs, command.Input.SkipVerify, source, result.EffectRef, accept, queue).Scan(&result.ID)
	return result, err
}
func (store *AcquisitionLinkStore) ReadPublishedLinkMutation(ctx context.Context, source string) (channelport.PublishedLinkMutation, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return channelport.PublishedLinkMutation{}, err
	}
	var result channelport.PublishedLinkMutation
	err = tx.QueryRow(ctx, `SELECT id,operation,requested_link_id,link_name,user_ids,department_ids,skip_verify FROM channel_acquisition_link_receipts WHERE source_ref_digest=$1`, source).Scan(&result.ReceiptID, &result.Operation, &result.LinkID, &result.LinkName, &result.UserIDs, &result.DepartmentIDs, &result.SkipVerify)
	return result, err
}
func (store *AcquisitionLinkStore) CompleteLinkMutation(ctx context.Context, c channelport.LinkMutationCompletion) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	var operation string
	if err = tx.QueryRow(ctx, `SELECT operation FROM channel_acquisition_link_receipts WHERE effect_ref=$1 FOR UPDATE`, c.EffectRef).Scan(&operation); err != nil {
		return err
	}
	if c.State == "executed" && operation != "delete" && (!validLinkID(c.LinkID) || !validHTTPSURL(c.URL)) {
		return ErrInvalidCatalogCommand
	}
	result, err := tx.Exec(ctx, `UPDATE channel_acquisition_link_receipts SET state=$2,result_link_id=$3,result_url=$4,outcome_digest=$5,business_endpoint_dispatched=$6,real_external_call_executed=$7,updated_at=$8 WHERE effect_ref=$1 AND state IN ('accepted','attempted','outcome_unknown')`, c.EffectRef, c.State, c.LinkID, c.URL, c.OutcomeDigest, c.BusinessEndpointDispatched, c.RealExternalCallExecuted, c.CompletedAt.UTC())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrCatalogConflict
	}
	return nil
}

func (store *AcquisitionLinkStore) ReceiptForReconcile(ctx context.Context, receiptID int64, linkID string) (AcquisitionLinkReceipt, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return AcquisitionLinkReceipt{}, err
	}
	var result AcquisitionLinkReceipt
	err = tx.QueryRow(ctx, `SELECT id,operation,requested_link_id,state,effect_ref,outcome_digest,business_endpoint_dispatched,real_external_call_executed,COALESCE(resolution,'') FROM channel_acquisition_link_receipts WHERE id=$1 FOR UPDATE`, receiptID).Scan(&result.ID, &result.Operation, &result.RequestedLinkID, &result.State, &result.EffectRef, &result.OutcomeDigest, &result.BusinessEndpointDispatched, &result.RealExternalCallExecuted, &result.Resolution)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrCatalogNotFound
	}
	if err != nil {
		return result, err
	}
	if result.Operation != "create" && result.RequestedLinkID != linkID {
		return result, ErrCatalogConflict
	}
	return result, nil
}

func (store *AcquisitionLinkStore) ReplayLinkReconciliation(ctx context.Context, command AcquisitionLinkReconcileCommand) (AcquisitionLinkReceipt, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return AcquisitionLinkReceipt{}, false, err
	}
	key := sha256.Sum256([]byte(command.IdempotencyKey))
	requestJSON, _ := json.Marshal(command)
	requestDigest := sha256.Sum256(requestJSON)
	var receiptID int64
	var oldRequest []byte
	err = tx.QueryRow(ctx, `SELECT receipt_id,request_digest FROM channel_acquisition_link_reconciliations WHERE actor_admin_user_id=$1 AND operation_key_digest=$2`, command.ActorID, key[:]).Scan(&receiptID, &oldRequest)
	if errors.Is(err, pgx.ErrNoRows) {
		return AcquisitionLinkReceipt{}, false, nil
	}
	if err != nil {
		return AcquisitionLinkReceipt{}, false, err
	}
	if len(oldRequest) != 32 || string(oldRequest) != string(requestDigest[:]) || receiptID != command.ReceiptID {
		return AcquisitionLinkReceipt{}, false, ErrCatalogConflict
	}
	receipt, err := store.receiptByID(ctx, receiptID)
	return receipt, err == nil, err
}

func (store *AcquisitionLinkStore) ReconcileLinkMutation(ctx context.Context, command AcquisitionLinkReconcileCommand, link *wecomport.CustomerAcquisitionLink) (AcquisitionLinkReceipt, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return AcquisitionLinkReceipt{}, err
	}
	key := sha256.Sum256([]byte(command.IdempotencyKey))
	requestJSON, _ := json.Marshal(command)
	requestDigest := sha256.Sum256(requestJSON)
	var oldRequest []byte
	var existingID int64
	err = tx.QueryRow(ctx, `SELECT id,request_digest FROM channel_acquisition_link_reconciliations WHERE actor_admin_user_id=$1 AND operation_key_digest=$2`, command.ActorID, key[:]).Scan(&existingID, &oldRequest)
	if err == nil {
		if len(oldRequest) != 32 || string(oldRequest) != string(requestDigest[:]) {
			return AcquisitionLinkReceipt{}, ErrCatalogConflict
		}
		return store.receiptByID(ctx, command.ReceiptID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AcquisitionLinkReceipt{}, err
	}
	current, err := store.ReceiptForReconcile(ctx, command.ReceiptID, command.LinkID)
	if err != nil {
		return AcquisitionLinkReceipt{}, err
	}
	if current.State != "outcome_unknown" {
		return AcquisitionLinkReceipt{}, ErrCatalogConflict
	}
	resultLinkID, resultURL := "", ""
	if command.Resolution == "provider_applied" {
		if current.Operation != "delete" {
			if link == nil || link.LinkID != command.LinkID || !validHTTPSURL(link.URL) {
				return AcquisitionLinkReceipt{}, ErrCatalogConflict
			}
			resultLinkID, resultURL = link.LinkID, link.URL
		} else if link != nil {
			return AcquisitionLinkReceipt{}, ErrCatalogConflict
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_acquisition_link_reconciliations(receipt_id,actor_admin_user_id,operation_key_digest,request_digest,resolution,evidence_digest) VALUES($1,$2,$3,$4,$5,$6)`, command.ReceiptID, command.ActorID, key[:], requestDigest[:], command.Resolution, command.EvidenceDigest); err != nil {
		return AcquisitionLinkReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE channel_acquisition_link_receipts SET state='reconciled',result_link_id=$2,result_url=$3,outcome_digest=$4,business_endpoint_dispatched=TRUE,real_external_call_executed=TRUE,resolution=$5,updated_at=clock_timestamp() WHERE id=$1 AND state='outcome_unknown'`, command.ReceiptID, resultLinkID, resultURL, command.EvidenceDigest, command.Resolution); err != nil {
		return AcquisitionLinkReceipt{}, err
	}
	return store.receiptByID(ctx, command.ReceiptID)
}

func (store *AcquisitionLinkStore) receiptByID(ctx context.Context, id int64) (AcquisitionLinkReceipt, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return AcquisitionLinkReceipt{}, err
	}
	var result AcquisitionLinkReceipt
	var linkID, linkName, resultURL string
	var userIDs []string
	var departmentIDs []int64
	var skipVerify bool
	err = tx.QueryRow(ctx, `SELECT id,operation,requested_link_id,state,effect_ref,outcome_digest,business_endpoint_dispatched,real_external_call_executed,COALESCE(resolution,''),COALESCE(NULLIF(result_link_id,''),requested_link_id),link_name,result_url,user_ids,department_ids,skip_verify FROM channel_acquisition_link_receipts WHERE id=$1`, id).Scan(&result.ID, &result.Operation, &result.RequestedLinkID, &result.State, &result.EffectRef, &result.OutcomeDigest, &result.BusinessEndpointDispatched, &result.RealExternalCallExecuted, &result.Resolution, &linkID, &linkName, &resultURL, &userIDs, &departmentIDs, &skipVerify)
	if err != nil {
		return result, err
	}
	if result.State == "executed" || result.State == "reconciled" && result.Resolution == "provider_applied" {
		if result.Operation != "delete" {
			result.Link = &wecomport.CustomerAcquisitionLink{LinkID: linkID, LinkName: linkName, URL: resultURL, UserIDs: userIDs, DepartmentIDs: departmentIDs, SkipVerify: skipVerify}
		}
	}
	return result, nil
}
func (store *AcquisitionLinkStore) List(ctx context.Context, before int64, limit int) ([]wecomport.CustomerAcquisitionLink, []int64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `WITH terminal AS (SELECT DISTINCT ON (COALESCE(NULLIF(result_link_id,''),requested_link_id)) id,operation,COALESCE(NULLIF(result_link_id,''),requested_link_id) link_id,link_name,result_url,user_ids,department_ids,skip_verify FROM channel_acquisition_link_receipts WHERE state IN ('executed','reconciled') ORDER BY COALESCE(NULLIF(result_link_id,''),requested_link_id),id DESC) SELECT id,link_id,link_name,result_url,user_ids,department_ids,skip_verify FROM terminal WHERE operation<>'delete' AND ($1::bigint=0 OR id<$1) ORDER BY id DESC LIMIT $2`, before, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []wecomport.CustomerAcquisitionLink{}
	ids := []int64{}
	for rows.Next() {
		var id int64
		var item wecomport.CustomerAcquisitionLink
		if err = rows.Scan(&id, &item.LinkID, &item.LinkName, &item.URL, &item.UserIDs, &item.DepartmentIDs, &item.SkipVerify); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		items = append(items, item)
	}
	return items, ids, rows.Err()
}
func (store *AcquisitionLinkStore) Get(ctx context.Context, id string) (wecomport.CustomerAcquisitionLink, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return wecomport.CustomerAcquisitionLink{}, err
	}
	var result wecomport.CustomerAcquisitionLink
	var operation string
	err = tx.QueryRow(ctx, `SELECT operation,COALESCE(NULLIF(result_link_id,''),requested_link_id),link_name,result_url,user_ids,department_ids,skip_verify FROM channel_acquisition_link_receipts WHERE COALESCE(NULLIF(result_link_id,''),requested_link_id)=$1 AND state IN ('executed','reconciled') ORDER BY id DESC LIMIT 1`, id).Scan(&operation, &result.LinkID, &result.LinkName, &result.URL, &result.UserIDs, &result.DepartmentIDs, &result.SkipVerify)
	if errors.Is(err, pgx.ErrNoRows) || operation == "delete" {
		return result, ErrCatalogNotFound
	}
	return result, err
}

func validLinkCommand(command AcquisitionLinkCommand) bool {
	if command.ActorID < 1 || !validOperationKey(command.IdempotencyKey) || (command.Operation != "create" && command.Operation != "update" && command.Operation != "delete") || command.Operation != "create" && !validLinkID(command.LinkID) {
		return false
	}
	if command.Operation == "delete" {
		return command.Input.LinkName == "" && len(command.Input.UserIDs) == 0 && len(command.Input.DepartmentIDs) == 0 && !command.Input.SkipVerify
	}
	return validLinkInput(command.Input)
}
func validLinkInput(input wecomport.CustomerAcquisitionLinkInput) bool {
	if input.LinkName == "" || strings.TrimSpace(input.LinkName) != input.LinkName || len([]rune(input.LinkName)) > 30 || len(input.UserIDs) > 500 || len(input.DepartmentIDs) > 500 || len(input.UserIDs)+len(input.DepartmentIDs) == 0 {
		return false
	}
	seen := map[string]struct{}{}
	for _, id := range input.UserIDs {
		if !validLinkID(id) {
			return false
		}
		if _, ok := seen[id]; ok {
			return false
		}
		seen[id] = struct{}{}
	}
	departments := map[int64]struct{}{}
	for _, id := range input.DepartmentIDs {
		if id < 1 {
			return false
		}
		if _, ok := departments[id]; ok {
			return false
		}
		departments[id] = struct{}{}
	}
	return true
}
func validLinkID(value string) bool {
	return value != "" && len(value) <= 1024 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "/\\\x00\n\r")
}

func validHTTPSURL(value string) bool {
	return strings.HasPrefix(value, "https://") && len(value) <= 10000 && !strings.ContainsAny(value, "\x00\r\n")
}

var _ channelport.PublishedLinkMutationReader = (*AcquisitionLinkStore)(nil)
var _ channelport.LinkMutationCompletionWriter = (*AcquisitionLinkStore)(nil)
