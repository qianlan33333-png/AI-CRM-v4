package adapter

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/archivesdk"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"testing"
)

func TestArchiveDecryptStartsOneBoundedRunnerForABatch(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	encrypted := make([]wecomport.EncryptedArchiveRecord, 3)
	for i := range encrypted {
		wrapped, err := rsa.EncryptPKCS1v15(rand.Reader, &key.PublicKey, []byte("random-key"))
		if err != nil {
			t.Fatal(err)
		}
		encrypted[i] = wecomport.EncryptedArchiveRecord{Seq: uint64(i + 1), MsgID: string(rune('a' + i)), PublicKeyVersion: 1, EncryptedKey: base64.StdEncoding.EncodeToString(wrapped), EncryptedMessage: "cipher"}
	}
	starts := 0
	reader := &MessageArchiveReader{config: MessageArchiveConfig{Enabled: true, CorpID: "wx-corp"}, keys: map[uint32]*rsa.PrivateKey{1: key}, invoke: func(_ context.Context, request archivesdk.Request) (archivesdk.Response, error) {
		starts++
		if request.Operation != "decrypt_batch" || len(request.DecryptItems) != 3 {
			t.Fatalf("request=%+v", request)
		}
		items := make([][]byte, 3)
		for i := range items {
			items[i], _ = json.Marshal(map[string]any{"msgid": encrypted[i].MsgID, "from": "wm_fixture", "tolist": []string{"employee"}, "msgtype": "text", "msgtime": 1, "text": map[string]string{"content": "x"}})
		}
		return archivesdk.Response{Items: items}, nil
	}}
	plain, err := reader.DecryptArchiveData(context.Background(), encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 || len(plain) != 3 {
		t.Fatalf("starts=%d plain=%d", starts, len(plain))
	}
	if len(plain[0].ExternalIdentities) != 1 || plain[0].ExternalIdentities[0].Fact.Reference().Scope != "wecom-corp:wx-corp" {
		t.Fatalf("trusted=%+v", plain[0].ExternalIdentities)
	}
}
func TestArchiveTrustedExternalRecognizesFrozenWMAndWOOnly(t *testing.T) {
	reader := &MessageArchiveReader{config: MessageArchiveConfig{CorpID: "wx-corp"}}
	payload := []byte(`{"from":"wo_fixture","tolist":["wm_fixture","employee","wb_robot"]}`)
	facts, err := reader.trustedExternalIdentities(payload)
	if err != nil || len(facts) != 2 {
		t.Fatalf("facts=%+v err=%v", facts, err)
	}
}
