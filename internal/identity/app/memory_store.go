package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

var ErrMergeNotReversible = errors.New("merge is not reversible")

type memoryCustomer struct {
	ID       customerdomain.CustomerID
	Status   customerdomain.Status
	MergedTo customerdomain.CustomerID
	Version  int64
	Lineage  int64
}

type memoryIdentity struct {
	StoredIdentity
	Active  bool
	Version int64
}

type memoryLinkIntent struct {
	ID                     int64
	Hash                   string
	Command                LinkIntentCommand
	SourceVersion          int64
	Status                 string
	ExpiresAt              time.Time
	ConsumptionFingerprint string
	Result                 LinkResult
}

type mergeIdentityMember struct {
	IdentityID   int64
	From         customerdomain.CustomerID
	To           customerdomain.CustomerID
	VersionAfter int64
}

// MemoryStore serializes every operation with a mutex. It is a test double for
// the Store transaction contract, not a production persistence implementation.
type MemoryStore struct {
	mu sync.Mutex

	nextCustomerID  customerdomain.CustomerID
	nextIdentityID  int64
	nextCandidateID int64
	nextConflictID  int64
	nextMergeID     int64
	nextIntentID    int64

	customers       map[customerdomain.CustomerID]memoryCustomer
	identities      map[int64]memoryIdentity
	identityByKey   map[string]int64
	candidates      map[int64]MergeCandidate
	candidateByPair map[string]int64
	conflicts       map[string]Conflict
	merges          map[int64]MergeRecord
	movedByMerge    map[int64][]mergeIdentityMember
	intentsByHash   map[string]memoryLinkIntent
	phoneReceipts   map[string]memoryDeclaredReceipt
}

type memoryDeclaredReceipt struct {
	Digest [32]byte
	Result identityport.DeclaredAttachResult
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		customers:       make(map[customerdomain.CustomerID]memoryCustomer),
		identities:      make(map[int64]memoryIdentity),
		identityByKey:   make(map[string]int64),
		candidates:      make(map[int64]MergeCandidate),
		candidateByPair: make(map[string]int64),
		conflicts:       make(map[string]Conflict),
		merges:          make(map[int64]MergeRecord),
		movedByMerge:    make(map[int64][]mergeIdentityMember),
		intentsByHash:   make(map[string]memoryLinkIntent),
		phoneReceipts:   make(map[string]memoryDeclaredReceipt),
	}
}

func (store *MemoryStore) AttachDeclaredPhone(_ context.Context, command identityport.DeclaredPhoneCommand, reference identitydomain.NormalizedReference) (identityport.DeclaredAttachResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	digest := sha256.Sum256([]byte(reference.NormalizedValue))
	if receipt, ok := store.phoneReceipts[command.IdempotencyKey]; ok {
		if receipt.Digest != digest {
			return identityport.DeclaredAttachResult{}, ErrDeclaredPayloadMismatch
		}
		replayed := receipt.Result
		replayed.ReplayOf = replayed.Status
		replayed.Status = identityport.DeclaredReplayed
		return replayed, nil
	}
	result := identityport.DeclaredAttachResult{CustomerID: command.CustomerID}
	if store.activeRootLocked(command.CustomerID) != command.CustomerID {
		result.Status = identityport.DeclaredInvalid
	} else if id, ok := store.identityByKey[identityKey(reference)]; ok {
		result.IdentityID = id
		if store.identities[id].CustomerID == command.CustomerID {
			result.Status = identityport.DeclaredAlreadyLinked
		} else {
			result.Status = identityport.DeclaredConflict
		}
	} else {
		result.Status = identityport.DeclaredAttached
		result.IdentityID = store.createIdentityLocked(command.CustomerID, reference)
	}
	store.phoneReceipts[command.IdempotencyKey] = memoryDeclaredReceipt{Digest: digest, Result: result}
	return result, nil
}

func (store *MemoryStore) Resolve(_ context.Context, reference identitydomain.NormalizedReference) (StoredIdentity, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	identityID, found := store.identityByKey[identityKey(reference)]
	if !found {
		return StoredIdentity{}, false, nil
	}
	identity := store.identities[identityID]
	if !identity.Active {
		return StoredIdentity{}, false, nil
	}
	identity.CustomerID = store.rootLocked(identity.CustomerID)
	return identity.StoredIdentity, true, nil
}

func (store *MemoryStore) Provision(_ context.Context, fact identitydomain.VerifiedFact) (ProvisionedIdentity, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	reference := fact.Reference()
	if existingID, found := store.identityByKey[identityKey(reference)]; found {
		existing := store.identities[existingID]
		return ProvisionedIdentity{CustomerID: store.rootLocked(existing.CustomerID), IdentityID: existingID}, nil
	}
	customerID := store.createCustomerLocked()
	identityID := store.createIdentityLocked(customerID, reference)
	return ProvisionedIdentity{CustomerID: customerID, IdentityID: identityID, Created: true}, nil
}

func (store *MemoryStore) Link(_ context.Context, command LinkCommand) (LinkResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.linkLocked(command)
}

func (store *MemoryStore) CreateLinkIntent(_ context.Context, command LinkIntentCommand) (CreatedLinkIntent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	sourceRoot := store.activeRootLocked(command.SourceCustomerID)
	if sourceRoot < 1 {
		return CreatedLinkIntent{}, ErrInvalidLinkCommand
	}
	// Bind the one-time authorization to the active root that existed when it
	// was issued. A later merge must invalidate it rather than silently retarget
	// the authorization to another customer.
	command.SourceCustomerID = sourceRoot
	rawToken, err := randomLinkToken()
	if err != nil {
		return CreatedLinkIntent{}, err
	}
	hash := linkTokenHash(rawToken)
	store.nextIntentID++
	intent := memoryLinkIntent{
		ID: store.nextIntentID, Hash: hash, Command: command,
		SourceVersion: store.customers[sourceRoot].Version,
		Status:        "pending", ExpiresAt: command.ExpiresAt,
	}
	store.intentsByHash[hash] = intent
	return CreatedLinkIntent{ID: intent.ID, Token: rawToken, ExpiresAt: intent.ExpiresAt}, nil
}

func (store *MemoryStore) ConsumeLinkIntent(_ context.Context, command ConsumeLinkIntentCommand) (LinkResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	hash := linkTokenHash(command.Token)
	intent, found := store.intentsByHash[hash]
	if !found {
		return LinkResult{Status: LinkIntentReplay}, nil
	}
	fingerprint := linkIntentConsumptionFingerprint(command)
	if intent.Status == "consumed" {
		if intent.ConsumptionFingerprint != fingerprint {
			return LinkResult{}, ErrLinkIntentPayloadMismatch
		}
		return replayLinkResult(intent.Result), nil
	}
	if intent.Status == "expired" {
		return LinkResult{Status: LinkIntentExpired}, nil
	}
	if intent.Status == "cancelled" {
		return LinkResult{Status: LinkIntentInvalidated}, nil
	}
	if intent.Status != "pending" {
		return LinkResult{Status: LinkIntentReplay}, nil
	}
	if !intent.ExpiresAt.After(time.Now()) {
		intent.Status = "expired"
		store.intentsByHash[hash] = intent
		return LinkResult{Status: LinkIntentExpired}, nil
	}
	reference := command.Target.Reference()
	if reference.Kind != intent.Command.TargetKind || (intent.Command.ExpectedScope != "" && reference.Scope != intent.Command.ExpectedScope) {
		return LinkResult{Status: LinkScopeMismatch}, nil
	}
	if store.activeRootLocked(intent.Command.SourceCustomerID) != intent.Command.SourceCustomerID ||
		store.customers[intent.Command.SourceCustomerID].Version != intent.SourceVersion {
		intent.Status = "cancelled"
		store.intentsByHash[hash] = intent
		return LinkResult{Status: LinkIntentInvalidated}, nil
	}
	result, err := store.linkLocked(LinkCommand{
		SourceCustomerID: intent.Command.SourceCustomerID,
		Target:           command.Target,
		Evidence:         command.Evidence,
	})
	if err != nil {
		return LinkResult{}, err
	}
	intent.Status = "consumed"
	intent.ConsumptionFingerprint = fingerprint
	intent.Result = cloneLinkResult(result)
	store.intentsByHash[hash] = intent
	return result, nil
}

func (store *MemoryStore) linkLocked(command LinkCommand) (LinkResult, error) {
	sourceRoot := store.activeRootLocked(command.SourceCustomerID)
	if sourceRoot < 1 {
		return LinkResult{}, ErrInvalidLinkCommand
	}
	reference := command.Target.Reference()
	identityID, exists := store.identityByKey[identityKey(reference)]
	if !exists {
		if command.Evidence.Strength != identitydomain.EvidenceStrong {
			return LinkResult{}, ErrInsufficientLinkEvidence
		}
		if reason := store.sameRootStrongConflictLocked(sourceRoot, reference); reason != "" {
			conflict := store.createConflictLocked(sourceRoot, sourceRoot, reason, command.Evidence)
			return LinkResult{Status: LinkConflict, CustomerID: sourceRoot, Conflict: &conflict}, nil
		}
		identityID = store.createIdentityLocked(sourceRoot, reference)
		store.touchCustomerLocked(sourceRoot)
		return LinkResult{Status: LinkAttached, CustomerID: sourceRoot, IdentityID: identityID}, nil
	}
	target := store.identities[identityID]
	targetRoot := store.rootLocked(target.CustomerID)
	if targetRoot == sourceRoot {
		return LinkResult{Status: LinkAlreadyLinked, CustomerID: sourceRoot, IdentityID: identityID}, nil
	}

	reason := "cross_root_link_requires_confirmation"
	if command.Evidence.Strength != identitydomain.EvidenceStrong {
		reason = "non_strong_evidence"
	}
	candidate := store.createCandidateLocked(sourceRoot, targetRoot, command.Evidence, reason)
	return LinkResult{Status: LinkCandidate, CustomerID: sourceRoot, IdentityID: identityID, Candidate: &candidate}, nil
}

func (store *MemoryStore) ConfirmMerge(_ context.Context, command ConfirmMergeCommand) (LinkResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	candidate, found := store.candidates[command.CandidateID]
	if !found || candidate.Status != "open" || candidate.Evidence.Strength != identitydomain.EvidenceStrong {
		return LinkResult{}, ErrInvalidLinkCommand
	}
	left, right := candidate.LeftCustomerID, candidate.RightCustomerID
	if left == right || (command.SurvivorCustomerID != left && command.SurvivorCustomerID != right) {
		return LinkResult{}, ErrInvalidLinkCommand
	}
	leftCustomer, leftFound := store.customers[candidate.LeftCustomerID]
	rightCustomer, rightFound := store.customers[candidate.RightCustomerID]
	if !leftFound || !rightFound || leftCustomer.Status != customerdomain.StatusActive ||
		rightCustomer.Status != customerdomain.StatusActive || leftCustomer.Version != candidate.LeftVersion ||
		rightCustomer.Version != candidate.RightVersion {
		candidate = store.rejectCandidateLocked(candidate)
		return LinkResult{Status: LinkCandidateRejected, CustomerID: command.SurvivorCustomerID, Candidate: &candidate}, nil
	}
	if reason := store.strongConflictLocked(left, right); reason != "" {
		conflict := store.createConflictLocked(left, right, reason, candidate.Evidence)
		candidate = store.rejectCandidateLocked(candidate)
		return LinkResult{Status: LinkConflict, CustomerID: command.SurvivorCustomerID, Candidate: &candidate, Conflict: &conflict}, nil
	}
	if store.hasWeComLocked(left) != store.hasWeComLocked(right) {
		wecomRoot := left
		if !store.hasWeComLocked(left) {
			wecomRoot = right
		}
		if command.SurvivorCustomerID != wecomRoot {
			return LinkResult{}, ErrInvalidLinkCommand
		}
	}
	loser := left
	if command.SurvivorCustomerID == left {
		loser = right
	}
	merge := store.mergeLocked(candidate.ID, loser, command.SurvivorCustomerID, candidate.Evidence, command.Operator)
	candidate.Status = "confirmed"
	store.candidates[candidate.ID] = candidate
	delete(store.candidateByPair, candidatePairKey(candidate.LeftCustomerID, candidate.RightCustomerID))
	return LinkResult{Status: LinkMerged, CustomerID: command.SurvivorCustomerID, Candidate: &candidate, Merge: &merge}, nil
}

func (store *MemoryStore) RevertMerge(_ context.Context, mergeID int64) (MergeRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	merge, found := store.merges[mergeID]
	if !found || merge.Reversed {
		return MergeRecord{}, ErrMergeNotReversible
	}
	from := store.customers[merge.FromCustomerID]
	if from.Status != customerdomain.StatusMerged || from.MergedTo != merge.ToCustomerID {
		return MergeRecord{}, ErrMergeNotReversible
	}
	to, toFound := store.customers[merge.ToCustomerID]
	if !toFound || to.Status != customerdomain.StatusActive ||
		from.Lineage != merge.FromLineageAfter || to.Lineage != merge.ToLineageAfter {
		return MergeRecord{}, ErrMergeNotReversible
	}
	members := store.movedByMerge[mergeID]
	// Validate every compare-and-swap precondition before mutating any member.
	// This prevents a partial reversal if an identity moved or was retired after
	// the merge. Identities attached later are absent from this snapshot.
	for _, member := range members {
		identity, identityFound := store.identities[member.IdentityID]
		if !identityFound || !identity.Active || identity.CustomerID != member.To || identity.Version != member.VersionAfter {
			return MergeRecord{}, ErrMergeNotReversible
		}
	}
	for _, member := range members {
		identity := store.identities[member.IdentityID]
		identity.CustomerID = member.From
		identity.Version++
		store.identities[member.IdentityID] = identity
	}
	from.Status = customerdomain.StatusActive
	from.MergedTo = 0
	from.Version++
	from.Lineage++
	store.customers[from.ID] = from
	to.Version++
	to.Lineage++
	store.customers[to.ID] = to
	merge.Reversed = true
	store.merges[mergeID] = merge
	return merge, nil
}

func (store *MemoryStore) CustomerCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.customers)
}

func (store *MemoryStore) ActiveIdentityCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, identity := range store.identities {
		if identity.Active {
			count++
		}
	}
	return count
}

func (store *MemoryStore) Root(customerID customerdomain.CustomerID) customerdomain.CustomerID {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.rootLocked(customerID)
}

func (store *MemoryStore) createCustomerLocked() customerdomain.CustomerID {
	store.nextCustomerID++
	customer := memoryCustomer{ID: store.nextCustomerID, Status: customerdomain.StatusActive, Version: 1, Lineage: 1}
	store.customers[customer.ID] = customer
	return customer.ID
}

func (store *MemoryStore) createIdentityLocked(customerID customerdomain.CustomerID, reference identitydomain.NormalizedReference) int64 {
	store.nextIdentityID++
	identity := memoryIdentity{StoredIdentity: StoredIdentity{
		ID: store.nextIdentityID, CustomerID: customerID, Reference: reference,
	}, Active: true, Version: 1}
	store.identities[identity.ID] = identity
	store.identityByKey[identityKey(reference)] = identity.ID
	return identity.ID
}

func (store *MemoryStore) activeRootLocked(customerID customerdomain.CustomerID) customerdomain.CustomerID {
	root := store.rootLocked(customerID)
	if root < 1 || store.customers[root].Status != customerdomain.StatusActive {
		return 0
	}
	return root
}

func (store *MemoryStore) touchCustomerLocked(customerID customerdomain.CustomerID) {
	customer := store.customers[customerID]
	customer.Version++
	store.customers[customerID] = customer
}

func (store *MemoryStore) rootLocked(customerID customerdomain.CustomerID) customerdomain.CustomerID {
	seen := map[customerdomain.CustomerID]struct{}{}
	for customerID > 0 {
		if _, loop := seen[customerID]; loop {
			return 0
		}
		seen[customerID] = struct{}{}
		customer, found := store.customers[customerID]
		if !found {
			return 0
		}
		if customer.Status != customerdomain.StatusMerged {
			return customerID
		}
		customerID = customer.MergedTo
	}
	return 0
}

func (store *MemoryStore) hasWeComLocked(customerID customerdomain.CustomerID) bool {
	for _, identity := range store.identities {
		if identity.Active && store.rootLocked(identity.CustomerID) == customerID && identity.Reference.Kind == identitydomain.KindWeComExternalUserID {
			return true
		}
	}
	return false
}

func (store *MemoryStore) strongConflictLocked(left, right customerdomain.CustomerID) string {
	if store.hasWeComLocked(left) && store.hasWeComLocked(right) {
		return "two_wecom_roots"
	}
	for _, leftIdentity := range store.identities {
		if !leftIdentity.Active || store.rootLocked(leftIdentity.CustomerID) != left || !singleValueStrong(leftIdentity.Reference.Kind) {
			continue
		}
		for _, rightIdentity := range store.identities {
			if rightIdentity.Active && store.rootLocked(rightIdentity.CustomerID) == right &&
				rightIdentity.Reference.Kind == leftIdentity.Reference.Kind &&
				rightIdentity.Reference.Scope == leftIdentity.Reference.Scope &&
				rightIdentity.Reference.NormalizedValue != leftIdentity.Reference.NormalizedValue {
				return "single_value_strong_namespace"
			}
		}
	}
	return ""
}

func (store *MemoryStore) sameRootStrongConflictLocked(customerID customerdomain.CustomerID, reference identitydomain.NormalizedReference) string {
	if !singleValueStrong(reference.Kind) {
		return ""
	}
	for _, identity := range store.identities {
		if !identity.Active || store.rootLocked(identity.CustomerID) != customerID ||
			identity.Reference.Kind != reference.Kind || identity.Reference.Scope != reference.Scope ||
			identity.Reference.NormalizedValue == reference.NormalizedValue {
			continue
		}
		if reference.Kind == identitydomain.KindWeComExternalUserID {
			return "two_wecom_identities_same_root"
		}
		return "single_value_strong_namespace"
	}
	return ""
}

func (store *MemoryStore) mergeLocked(candidateID int64, loser, survivor customerdomain.CustomerID, evidence identitydomain.LinkEvidence, operator string) MergeRecord {
	store.nextMergeID++
	loserCustomer := store.customers[loser]
	survivorCustomer := store.customers[survivor]
	loserVersionBefore := loserCustomer.Version
	survivorVersionBefore := survivorCustomer.Version
	loserLineageBefore := loserCustomer.Lineage
	survivorLineageBefore := survivorCustomer.Lineage
	moved := make([]mergeIdentityMember, 0)
	for identityID, identity := range store.identities {
		if identity.Active && store.rootLocked(identity.CustomerID) == loser {
			identity.CustomerID = survivor
			identity.Version++
			store.identities[identityID] = identity
			moved = append(moved, mergeIdentityMember{
				IdentityID: identityID, From: loser, To: survivor, VersionAfter: identity.Version,
			})
		}
	}
	sort.Slice(moved, func(i, j int) bool { return moved[i].IdentityID < moved[j].IdentityID })
	loserCustomer.Status = customerdomain.StatusMerged
	loserCustomer.MergedTo = survivor
	loserCustomer.Version++
	loserCustomer.Lineage++
	store.customers[loser] = loserCustomer
	survivorCustomer.Version++
	survivorCustomer.Lineage++
	store.customers[survivor] = survivorCustomer
	merge := MergeRecord{
		ID: store.nextMergeID, CandidateID: candidateID,
		FromCustomerID: loser, ToCustomerID: survivor,
		FromVersionBefore: loserVersionBefore, FromVersionAfter: loserCustomer.Version,
		ToVersionBefore: survivorVersionBefore, ToVersionAfter: survivorCustomer.Version,
		FromLineageBefore: loserLineageBefore, FromLineageAfter: loserCustomer.Lineage,
		ToLineageBefore: survivorLineageBefore, ToLineageAfter: survivorCustomer.Lineage,
		Evidence: evidence, Rule: "confirmed_candidate", Operator: operator,
	}
	store.merges[merge.ID] = merge
	store.movedByMerge[merge.ID] = moved
	return merge
}

func (store *MemoryStore) createCandidateLocked(left, right customerdomain.CustomerID, evidence identitydomain.LinkEvidence, reason string) MergeCandidate {
	key := candidatePairKey(left, right)
	if existingID, found := store.candidateByPair[key]; found {
		existing := store.candidates[existingID]
		if existing.Status == "open" && store.candidateVersionsMatchLocked(existing) {
			if evidenceRank(evidence.Strength) > evidenceRank(existing.Evidence.Strength) {
				existing.Evidence = evidence
				existing.Reason = reason
				store.candidates[existing.ID] = existing
			}
			return existing
		}
		store.rejectCandidateLocked(existing)
	}
	store.nextCandidateID++
	candidate := MergeCandidate{
		ID: store.nextCandidateID, LeftCustomerID: left, RightCustomerID: right,
		Evidence: evidence, Reason: reason, Status: "open",
		LeftVersion: store.customers[left].Version, RightVersion: store.customers[right].Version,
	}
	store.candidates[candidate.ID] = candidate
	store.candidateByPair[key] = candidate.ID
	return candidate
}

func (store *MemoryStore) candidateVersionsMatchLocked(candidate MergeCandidate) bool {
	left, leftFound := store.customers[candidate.LeftCustomerID]
	right, rightFound := store.customers[candidate.RightCustomerID]
	return leftFound && rightFound && left.Status == customerdomain.StatusActive && right.Status == customerdomain.StatusActive &&
		left.Version == candidate.LeftVersion && right.Version == candidate.RightVersion
}

func (store *MemoryStore) rejectCandidateLocked(candidate MergeCandidate) MergeCandidate {
	candidate.Status = "rejected"
	store.candidates[candidate.ID] = candidate
	delete(store.candidateByPair, candidatePairKey(candidate.LeftCustomerID, candidate.RightCustomerID))
	return candidate
}

func (store *MemoryStore) createConflictLocked(left, right customerdomain.CustomerID, reason string, evidence identitydomain.LinkEvidence) Conflict {
	if right < left {
		left, right = right, left
	}
	key := strconv.FormatInt(int64(left), 10) + ":" + strconv.FormatInt(int64(right), 10) + ":" + reason
	if existing, found := store.conflicts[key]; found {
		return existing
	}
	store.nextConflictID++
	conflict := Conflict{ID: store.nextConflictID, LeftCustomerID: left, RightCustomerID: right, Reason: reason, Evidence: evidence}
	store.conflicts[key] = conflict
	return conflict
}

func identityKey(reference identitydomain.NormalizedReference) string {
	return string(reference.Kind) + "\x00" + reference.Scope + "\x00" + reference.NormalizedValue
}

func candidatePairKey(left, right customerdomain.CustomerID) string {
	// Canonicalization is only for open-pair uniqueness. It never selects a
	// surviving customer; confirmation must name an endpoint explicitly.
	if right < left {
		left, right = right, left
	}
	return strconv.FormatInt(int64(left), 10) + ":" + strconv.FormatInt(int64(right), 10)
}

func evidenceRank(strength identitydomain.EvidenceStrength) int {
	switch strength {
	case identitydomain.EvidenceStrong:
		return 3
	case identitydomain.EvidenceMedium:
		return 2
	case identitydomain.EvidenceWeak:
		return 1
	default:
		return 0
	}
}

func singleValueStrong(kind identitydomain.Kind) bool {
	switch kind {
	case identitydomain.KindWeComExternalUserID, identitydomain.KindUnionID,
		identitydomain.KindMPOpenID, identitydomain.KindOAOpenID,
		identitydomain.KindAlipayUserID, identitydomain.KindAlipayOAuthUserID,
		identitydomain.KindAlipayOAuthOpenID, identitydomain.KindAlipayBuyerID,
		identitydomain.KindAlipayBuyerOpenID, identitydomain.KindFirstPartyMemberID:
		return true
	default:
		return false
	}
}

func randomLinkToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func linkTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawStdEncoding.EncodeToString(digest[:])
}

func linkIntentConsumptionFingerprint(command ConsumeLinkIntentCommand) string {
	reference := command.Target.Reference()
	fields := []string{
		string(reference.Kind), reference.Scope, reference.NormalizedValue,
		string(reference.Assurance), reference.Source, strconv.FormatInt(int64(reference.NormalizerVersion), 10),
		command.Evidence.Type, string(command.Evidence.Strength), command.Evidence.Source,
		command.Evidence.EventID, command.Evidence.Digest, command.Evidence.PolicyVersion,
	}
	hasher := sha256.New()
	for _, field := range fields {
		_, _ = hasher.Write([]byte(strconv.Itoa(len(field))))
		_, _ = hasher.Write([]byte{':'})
		_, _ = hasher.Write([]byte(field))
	}
	return base64.RawStdEncoding.EncodeToString(hasher.Sum(nil))
}

func replayLinkResult(original LinkResult) LinkResult {
	replay := cloneLinkResult(original)
	replay.ReplayOf = original.Status
	replay.Status = LinkIntentReplay
	return replay
}

func cloneLinkResult(result LinkResult) LinkResult {
	cloned := result
	if result.Candidate != nil {
		candidate := *result.Candidate
		cloned.Candidate = &candidate
	}
	if result.Conflict != nil {
		conflict := *result.Conflict
		cloned.Conflict = &conflict
	}
	if result.Merge != nil {
		merge := *result.Merge
		cloned.Merge = &merge
	}
	return cloned
}
