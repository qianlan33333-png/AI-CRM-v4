package app

import (
	"context"
	"encoding/hex"
	"errors"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

var (
	ErrHXCSourceNotReady     = errors.New("HXC identity source is not ready")
	ErrHXCInspectionMismatch = errors.New("HXC identity inspection returned an unexpected result count")
)

type HXCSourceStore interface {
	ReplayHXCResolution(context.Context, identityport.HXCSubject) (identityport.HXCSubjectResult, bool, error)
	PersistHXCResolution(context.Context, identityport.HXCSubject, identityport.HXCSubjectResult) (identityport.HXCSubjectResult, error)
	IgnoreHXCSourceConflict(context.Context, identityport.IgnoreHXCSourceConflictCommand) (identityport.HXCSourceConflict, bool, error)
	CompleteHXCSnapshot(context.Context, [][32]byte) error
}

type HXCVerifiedIdentityFactory interface {
	VerifiedHXCUnionID(scope, value string) (identitydomain.VerifiedFact, error)
}

type HXCSourceService struct {
	Inspector        identityport.HXCIdentityInspector
	Store            HXCSourceStore
	OneID            OneIDService
	VerifiedIdentity HXCVerifiedIdentityFactory
}

func (service HXCSourceService) InspectHXCSubjects(ctx context.Context, subjects []identityport.HXCSubject) ([]identityport.HXCSubjectResult, error) {
	if service.Inspector == nil {
		return nil, ErrHXCSourceNotReady
	}
	return service.Inspector.InspectHXCSubjects(ctx, subjects)
}

func (service HXCSourceService) ApplyHXCSubject(ctx context.Context, subject identityport.HXCSubject) (identityport.HXCSubjectResult, error) {
	if service.Inspector == nil || service.Store == nil || service.OneID.Store == nil || subject.RuleVersion == "" {
		return identityport.HXCSubjectResult{}, ErrHXCSourceNotReady
	}
	if replay, found, err := service.Store.ReplayHXCResolution(ctx, subject); err != nil {
		return identityport.HXCSubjectResult{}, err
	} else if found {
		replay.Position = subject.Position
		replay.Replayed = true
		return replay, nil
	}
	results, err := service.Inspector.InspectHXCSubjects(ctx, []identityport.HXCSubject{subject})
	if err != nil {
		return identityport.HXCSubjectResult{}, err
	}
	if len(results) != 1 {
		return identityport.HXCSubjectResult{}, ErrHXCInspectionMismatch
	}
	result := results[0]
	switch {
	case result.Disposition == identityport.HXCMatched && result.MatchedBy == identityport.HXCMatchUnionID && subject.Phone != "":
		attached, attachErr := service.OneID.AttachDeclaredPhoneToCustomer(ctx, identityport.DeclaredPhoneCommand{
			CustomerID: result.CustomerID, Phone: subject.Phone, Source: "hxc",
			SourceEventID:  "hxc:" + hex.EncodeToString(subject.SubjectDigest[:]),
			IdempotencyKey: "hxc-phone:" + hex.EncodeToString(subject.SubjectDigest[:]) + ":" + hex.EncodeToString(subject.PayloadDigest[:]),
		})
		if attachErr != nil {
			return identityport.HXCSubjectResult{}, attachErr
		}
		if attached.Status == identityport.DeclaredConflict || attached.Status == identityport.DeclaredInvalid {
			result = concurrentHXCConflict(subject)
		}
	case result.Disposition == identityport.HXCMatched && result.MatchedBy == identityport.HXCMatchPhone && subject.UnionID != "" && subject.UnionIDVerified:
		result, err = service.linkUnionID(ctx, subject, result)
		if err != nil {
			return identityport.HXCSubjectResult{}, err
		}
	case result.Disposition == identityport.HXCConflict && result.Reason == identityport.HXCReasonCrossRoot && subject.UnionIDVerified:
		result, err = service.linkUnionID(ctx, subject, result)
		if err != nil {
			return identityport.HXCSubjectResult{}, err
		}
	}
	return service.Store.PersistHXCResolution(ctx, subject, result)
}

func (service HXCSourceService) linkUnionID(ctx context.Context, subject identityport.HXCSubject, result identityport.HXCSubjectResult) (identityport.HXCSubjectResult, error) {
	if service.VerifiedIdentity == nil {
		return identityport.HXCSubjectResult{}, ErrHXCSourceNotReady
	}
	fact, err := service.VerifiedIdentity.VerifiedHXCUnionID(subject.UnionIDScope, subject.UnionID)
	if err != nil {
		result.Disposition, result.MatchedBy, result.CustomerID = identityport.HXCInvalid, identityport.HXCMatchNone, 0
		result.Reason = identityport.HXCReasonInvalidUnionID
		return result, nil
	}
	sourceCustomer := result.CustomerID
	if result.Disposition == identityport.HXCConflict {
		sourceCustomer = result.PhoneCustomerID
	}
	linked, err := service.OneID.LinkVerifiedIdentity(ctx, LinkCommand{
		SourceCustomerID: sourceCustomer,
		Target:           fact,
		Evidence: identitydomain.LinkEvidence{
			Type: "hxc_unionid_phone_pair", Strength: identitydomain.EvidenceStrong, Source: "hxc",
			EventID: "hxc:" + hex.EncodeToString(subject.SubjectDigest[:]),
			Digest:  hex.EncodeToString(subject.PayloadDigest[:]), PolicyVersion: subject.RuleVersion,
		},
	})
	if err != nil {
		return identityport.HXCSubjectResult{}, err
	}
	switch linked.Status {
	case LinkAttached, LinkAlreadyLinked:
		return result, nil
	case LinkCandidate:
		result.Disposition, result.MatchedBy, result.CustomerID = identityport.HXCConflict, identityport.HXCMatchNone, 0
		result.Reason = identityport.HXCReasonCrossRoot
		if linked.Candidate != nil {
			result.MergeCandidateID = linked.Candidate.ID
		}
		return result, nil
	default:
		return concurrentHXCConflict(subject), nil
	}
}

func concurrentHXCConflict(subject identityport.HXCSubject) identityport.HXCSubjectResult {
	return identityport.HXCSubjectResult{Position: subject.Position, Disposition: identityport.HXCConflict, MatchedBy: identityport.HXCMatchNone, Reason: identityport.HXCReasonConcurrentConflict}
}

func (service HXCSourceService) IgnoreHXCSourceConflict(ctx context.Context, command identityport.IgnoreHXCSourceConflictCommand) (identityport.HXCSourceConflict, bool, error) {
	if service.Store == nil {
		return identityport.HXCSourceConflict{}, false, ErrHXCSourceNotReady
	}
	return service.Store.IgnoreHXCSourceConflict(ctx, command)
}

func (service HXCSourceService) CompleteHXCSnapshot(ctx context.Context, seen [][32]byte) error {
	if service.Store == nil {
		return ErrHXCSourceNotReady
	}
	return service.Store.CompleteHXCSnapshot(ctx, seen)
}

var _ identityport.HXCIdentityCoordinator = HXCSourceService{}
var _ identityport.HXCSourceConflictManager = HXCSourceService{}
