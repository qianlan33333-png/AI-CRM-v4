package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrSidebarProfileConflict = errors.New("sidebar profile version conflict")
	ErrSidebarProfileInvalid  = errors.New("invalid sidebar profile command")
)

type SidebarProfileReceipt struct {
	PayloadDigest [32]byte
	Outcome       string
	Profile       customerport.SidebarProfile
}

type SidebarProfileStore interface {
	ReadSidebarProfile(context.Context, customerdomain.CustomerID) (customerport.SidebarProfile, error)
	FindSidebarProfileReceipt(context.Context, [32]byte) (SidebarProfileReceipt, bool, error)
	UpdateSidebarProfile(context.Context, customerport.SidebarProfileUpdate, [32]byte, [32]byte, time.Time) (customerport.SidebarProfile, error)
	RecordSidebarProfileReceipt(context.Context, [32]byte, [32]byte, customerport.SidebarProfileUpdate, string, customerport.SidebarProfile) error
}

type SidebarProfileApplication struct {
	Numbers    identityport.CustomerPublicNumbers
	uow        platformport.UnitOfWork
	store      SidebarProfileStore
	phones     identityport.DeclaredPhoneAttacher
	projection customerport.ProjectionWriter
	audit      interface {
		Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
	}
	outbox platformoutbox.Appender
	now    func() time.Time
}

func NewSidebarProfileApplication(uow platformport.UnitOfWork, store SidebarProfileStore, phones identityport.DeclaredPhoneAttacher, projection customerport.ProjectionWriter, audit interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}, outbox platformoutbox.Appender) (*SidebarProfileApplication, error) {
	if uow == nil || store == nil || phones == nil || projection == nil || audit == nil || outbox == nil {
		return nil, errors.New("sidebar profile dependencies are required")
	}
	return &SidebarProfileApplication{uow: uow, store: store, phones: phones, projection: projection, audit: audit, outbox: outbox, now: time.Now}, nil
}

func (service *SidebarProfileApplication) ReadSidebarProfile(ctx context.Context, customerID customerdomain.CustomerID) (customerport.SidebarProfile, error) {
	if customerID < 1 {
		return customerport.SidebarProfile{}, ErrSidebarProfileInvalid
	}
	var profile customerport.SidebarProfile
	err := service.uow.Within(ctx, func(txctx context.Context) error {
		var err error
		profile, err = service.store.ReadSidebarProfile(txctx, customerID)
		if err != nil {
			return err
		}
		if service.Numbers != nil {
			numbers, e := service.Numbers.CustomerPublicNumbers(txctx, []customerdomain.CustomerID{customerID})
			if e != nil {
				return e
			}
			profile.CustomerNumber = numbers[customerID]
			if profile.CustomerNumber == "" {
				return ErrNotFound
			}
		}
		return nil
	})
	return profile, err
}

func (service *SidebarProfileApplication) UpdateSidebarProfile(ctx context.Context, command customerport.SidebarProfileUpdate) (customerport.SidebarProfile, error) {
	command.DisplayName = strings.TrimSpace(command.DisplayName)
	command.CorpName = strings.TrimSpace(command.CorpName)
	command.ProfileSource = strings.TrimSpace(command.ProfileSource)
	command.Industry = strings.TrimSpace(command.Industry)
	command.IndustryDescription = strings.TrimSpace(command.IndustryDescription)
	command.NeedsBlockersFollowup = strings.TrimSpace(command.NeedsBlockersFollowup)
	annotationUpdate := command.SourceSet || command.IndustrySet || command.IndustryDescriptionSet || command.NeedsBlockersFollowupSet
	if command.CustomerID < 1 || command.EmployeeID == "" || len(command.EmployeeID) > 1024 ||
		(annotationUpdate && (command.ExpectedProfileVersion < 0 || len(command.ProfileSource) > 200 || len(command.Industry) > 200 || len(command.IndustryDescription) > 2000 || len(command.NeedsBlockersFollowup) > 2000)) ||
		(!annotationUpdate && (command.ExpectedVersion < 1 || command.DisplayName == "" || len(command.DisplayName) > 200 || len(command.CorpName) > 200 || command.Gender < 0 || command.Gender > 2)) {
		return customerport.SidebarProfile{}, ErrSidebarProfileInvalid
	}
	key, err := idempotency.Parse(command.IdempotencyKey)
	if err != nil {
		return customerport.SidebarProfile{}, ErrSidebarProfileInvalid
	}
	payload := []byte(nil)
	if annotationUpdate {
		payload, _ = json.Marshal([]any{command.CustomerID, command.EmployeeID, command.ExpectedProfileVersion, command.ProfileSource, command.Industry, command.IndustryDescription, command.NeedsBlockersFollowup, command.SourceSet, command.IndustrySet, command.IndustryDescriptionSet, command.NeedsBlockersFollowupSet})
	} else {
		payload, _ = json.Marshal([]any{command.CustomerID, command.EmployeeID, command.DisplayName, command.Gender, command.CorpName, command.ExpectedVersion})
	}
	keyDigest, payloadDigest := sha256.Sum256([]byte(key)), sha256.Sum256(payload)
	var result customerport.SidebarProfile
	conflicted := false
	err = service.uow.Within(ctx, func(txctx context.Context) error {
		updateErr := func() error {
			receipt, found, findErr := service.store.FindSidebarProfileReceipt(txctx, keyDigest)
			if findErr != nil {
				return findErr
			}
			if found {
				if receipt.PayloadDigest != payloadDigest {
					return ErrSidebarProfileConflict
				}
				result = receipt.Profile
				if receipt.Outcome == "version_conflict" {
					conflicted = true
				}
				return nil
			}
			now := service.now().UTC()
			result, findErr = service.store.UpdateSidebarProfile(txctx, command, keyDigest, payloadDigest, now)
			if errors.Is(findErr, ErrSidebarProfileConflict) {
				current, readErr := service.store.ReadSidebarProfile(txctx, command.CustomerID)
				if readErr != nil {
					return readErr
				}
				if receiptErr := service.store.RecordSidebarProfileReceipt(txctx, keyDigest, payloadDigest, command, "version_conflict", current); receiptErr != nil {
					return receiptErr
				}
				result = current
				conflicted = true
				return nil
			}
			if findErr != nil {
				return findErr
			}
			if err := service.store.RecordSidebarProfileReceipt(txctx, keyDigest, payloadDigest, command, "updated", result); err != nil {
				return err
			}
			facts := map[string]any{"version": result.Version}
			if annotationUpdate {
				changed := make([]string, 0, 4)
				if command.SourceSet {
					changed = append(changed, "profile_source")
				}
				if command.IndustrySet {
					changed = append(changed, "industry")
				}
				if command.IndustryDescriptionSet {
					changed = append(changed, "industry_description")
				}
				if command.NeedsBlockersFollowupSet {
					changed = append(changed, "needs_blockers_followup")
				}
				// Directory source_version intentionally stays fixed for operating
				// annotations. Audit the independent profile revision and safe field
				// categories so a same-directory-version update remains observable.
				facts = map[string]any{"change_kind": "sidebar_profile_annotations", "changed_fields": changed, "profile_version": result.ProfileVersion}
			}
			return service.appendFacts(txctx, "profile_updated", command.CustomerID, command.EmployeeID, command.IdempotencyKey, now, facts)
		}()
		if updateErr != nil {
			return updateErr
		}
		if service.Numbers != nil {
			numbers, readErr := service.Numbers.CustomerPublicNumbers(txctx, []customerdomain.CustomerID{command.CustomerID})
			if readErr != nil {
				return readErr
			}
			result.CustomerNumber = numbers[command.CustomerID]
			if result.CustomerNumber == "" {
				return ErrSidebarProfileInvalid
			}
		}
		return nil
	})
	if err == nil && conflicted {
		err = ErrSidebarProfileConflict
	}
	return result, err
}

func (service *SidebarProfileApplication) BindSidebarPhone(ctx context.Context, command customerport.SidebarPhoneBind) (customerport.SidebarPhoneResult, error) {
	if command.CustomerID < 1 || command.EmployeeID == "" || len(command.EmployeeID) > 1024 {
		return customerport.SidebarPhoneResult{}, ErrSidebarProfileInvalid
	}
	if _, err := idempotency.Parse(command.IdempotencyKey); err != nil {
		return customerport.SidebarPhoneResult{}, ErrSidebarProfileInvalid
	}
	phone := strings.TrimSpace(command.Phone)
	if len(phone) != 11 || phone[0] != '1' {
		return customerport.SidebarPhoneResult{}, ErrSidebarProfileInvalid
	}
	for _, char := range phone {
		if char < '0' || char > '9' {
			return customerport.SidebarPhoneResult{}, ErrSidebarProfileInvalid
		}
	}
	result := customerport.SidebarPhoneResult{}
	err := service.uow.Within(ctx, func(txctx context.Context) error {
		now := service.now().UTC()
		sourceDigest := sha256.Sum256([]byte(command.EmployeeID + "\x00" + command.IdempotencyKey))
		attached, err := service.phones.AttachDeclaredPhoneToCustomer(txctx, identityport.DeclaredPhoneCommand{CustomerID: command.CustomerID, Phone: phone, Source: "sidebar", SourceEventID: "sidebar:" + hex.EncodeToString(sourceDigest[:]), IdempotencyKey: command.IdempotencyKey})
		if err != nil {
			return err
		}
		status := attached.Status
		if status == identityport.DeclaredReplayed {
			status = attached.ReplayOf
		}
		if status == identityport.DeclaredConflict || status == identityport.DeclaredInvalid {
			return ErrSidebarProfileConflict
		}
		masked := phone[:3] + "****" + phone[7:]
		if err = service.projection.UpdateDirectoryPhone(txctx, command.CustomerID, masked, identitydomain.AssuranceDeclared, now.UnixNano(), now); err != nil {
			return err
		}
		result = customerport.SidebarPhoneResult{Status: string(attached.Status), PhoneMasked: masked, PhoneAssurance: string(identitydomain.AssuranceDeclared)}
		return service.appendFacts(txctx, "phone_declared", command.CustomerID, command.EmployeeID, command.IdempotencyKey, now, map[string]any{"assurance": "declared", "result": result.Status})
	})
	return result, err
}

func (service *SidebarProfileApplication) appendFacts(ctx context.Context, action string, customerID customerdomain.CustomerID, employeeID, key string, at time.Time, payload any) error {
	raw, _ := json.Marshal(payload)
	factDigest := sha256.Sum256([]byte(key))
	factKey := hex.EncodeToString(factDigest[:])
	if _, err := service.audit.Append(ctx, platformaudit.Event{IdempotencyKey: idempotency.Key("sidebar-customer-audit:" + factKey), Action: "customer.sidebar." + action, ActorType: "wecom_employee", ActorID: employeeID, ResourceType: "customer", ResourceID: strconv.FormatInt(int64(customerID), 10), Payload: raw, OccurredAt: at}); err != nil && !errors.Is(err, platformaudit.ErrDuplicateEvent) {
		return err
	}
	_, err := service.outbox.Append(ctx, platformoutbox.Event{AggregateType: "customer", AggregateID: strconv.FormatInt(int64(customerID), 10), Type: "customer.sidebar." + action + ".v1", Version: 1, IdempotencyKey: "sidebar-customer-outbox:" + factKey, Payload: raw, OccurredAt: at})
	return err
}

var _ customerport.SidebarProfileService = (*SidebarProfileApplication)(nil)
