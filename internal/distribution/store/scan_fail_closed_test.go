package store

import (
	"errors"
	"testing"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type zeroDistributionScanner struct{}

func (zeroDistributionScanner) Scan(...any) error { return nil }

func TestStoreScannersRejectZeroRowsWhenScanSucceeds(t *testing.T) {
	scanner := zeroDistributionScanner{}
	checks := []struct {
		name string
		err  error
	}{
		{"policy", scannerPolicyError(scanner)},
		{"positive_id", scannerPositiveIDError(scanner)},
		{"distributor", scannerDistributorError(scanner)},
		{"attribution", scannerAttributionError(scanner)},
		{"commission", scannerCommissionError(scanner)},
		{"promotion_credential", scannerPromotionCredentialError(scanner)},
		{"active_agreement", scannerAgreementError(scanner)},
		{"browser_session", scannerBrowserSessionError(scanner)},
		{"admin_exception", scannerAdminExceptionError(scanner)},
		{"operation_receipt", scannerOperationReceiptError(scanner)},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if !errors.Is(check.err, distributionport.ErrUnavailable) {
				t.Fatalf("zero row error=%v, want unavailable", check.err)
			}
		})
	}
}

func scannerPolicyError(row rowScanner) error {
	_, err := scanPolicy(row)
	return err
}
func scannerPositiveIDError(row rowScanner) error {
	_, err := scanPositiveID(row)
	return err
}
func scannerDistributorError(row rowScanner) error {
	_, _, err := scanDistributor(row)
	return err
}
func scannerAttributionError(row rowScanner) error {
	_, err := scanAttribution(row)
	return err
}
func scannerCommissionError(row rowScanner) error {
	_, err := scanCommission(row)
	return err
}
func scannerPromotionCredentialError(row rowScanner) error {
	_, err := scanPromotionCredential(row)
	return err
}
func scannerAgreementError(row rowScanner) error {
	_, err := scanActiveAgreement(row)
	return err
}
func scannerBrowserSessionError(row rowScanner) error {
	_, err := scanBrowserSession(row)
	return err
}
func scannerAdminExceptionError(row rowScanner) error {
	_, err := scanAdminException(row)
	return err
}
func scannerOperationReceiptError(row rowScanner) error {
	_, err := scanOperationReceipt(row)
	return err
}

func TestAdminReconcileTargetUsesTrustedStoredReference(t *testing.T) {
	cases := []struct {
		name, kind, evidence, instruction string
		want                              distributionport.AdminReconcileTarget
	}{
		{name: "cancelled unfreeze without settlement", kind: "unfreeze_final_failed", evidence: "psunfreeze_42", want: distributionport.AdminReconcileTargetUnfreeze},
		{name: "paid unfreeze wins over split instruction", kind: "unfreeze_final_failed", evidence: "psunfreeze_43", instruction: "psinst_8", want: distributionport.AdminReconcileTargetUnfreeze},
		{name: "ordinary settlement", kind: "settlement_unknown", instruction: "psinst_8", want: distributionport.AdminReconcileTargetSplit},
		{name: "untrusted evidence", kind: "unfreeze_final_failed", evidence: "browser:psunfreeze_8", want: distributionport.AdminReconcileTargetNone},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := adminReconcileTarget(test.kind, test.evidence, test.instruction); got != test.want {
				t.Fatalf("target=%q want=%q", got, test.want)
			}
		})
	}
}
