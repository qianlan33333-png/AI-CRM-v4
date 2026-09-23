package port

import (
	"context"
	"errors"
)

// ReconcilePrivateMessageDelivery performs the complete paginated Provider
// read shared by reviewed Excel sends and direct audience pushes. Exactly one
// matching recipient row is required before delivery can be proven.
func ReconcilePrivateMessageDelivery(ctx context.Context, provider PrivateMessageDeliveryReader, receipt PrivateMessageDelivery) (PrivateMessageDelivery, error) {
	if provider == nil || receipt.MessageID == "" || receipt.SenderUserID == "" || receipt.ExternalUserID == "" {
		return receipt, errors.New("incomplete private message receipt")
	}
	cursor := ""
	seen := map[string]bool{}
	var match *PrivateMessageDelivery
	for {
		if seen[cursor] {
			return receipt, errors.New("receipt cursor repeated")
		}
		seen[cursor] = true
		page, err := provider.GetPrivateMessageSendResult(ctx, receipt.MessageID, receipt.SenderUserID, cursor)
		if err != nil {
			return receipt, err
		}
		for _, item := range page.Items {
			if item.ExternalUserID == receipt.ExternalUserID && item.SenderUserID == receipt.SenderUserID && item.MessageID == receipt.MessageID {
				if match != nil {
					return receipt, errors.New("ambiguous receipt")
				}
				copy := item
				match = &copy
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if match == nil {
		return receipt, errors.New("receipt not observed")
	}
	return *match, nil
}
