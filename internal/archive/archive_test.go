package archive

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"

	"github.com/uinaf/lincrawl/internal/linear"
)

func sampleSnapshot() linear.Snapshot {
	return linear.Snapshot{
		Teams:  []linear.Team{{ID: "t1", Key: "LIN", Name: "Lincrawl", UpdatedAt: "2026-05-19T00:00:00Z"}},
		States: []linear.WorkflowState{{ID: "s1", TeamID: "t1", Name: "Backlog", Type: "backlog"}},
		Users:  []linear.User{{ID: "u1", Name: "Sam"}},
		Labels: []linear.Label{{ID: "l1", TeamID: "t1", Name: "ingest"}},
		Projects: []linear.Project{
			{ID: "p1", Name: "MVP", State: "started", UpdatedAt: "2026-05-19T00:00:00Z"},
		},
		Issues: []linear.Issue{
			{
				ID: "i1", Identifier: "LIN-1", Title: "Ingest", Description: "Body",
				TeamID: "t1", ProjectID: "p1", StateID: "s1", AssigneeID: "u1",
				CreatorID: "u1", Priority: 2, LabelIDs: []string{"l1"},
				CreatedAt: "2026-05-19T00:00:00Z", UpdatedAt: "2026-05-19T00:00:01Z",
				Comments: []linear.Comment{
					{
						ID: "c1", IssueID: "i1", AuthorID: "u1", Body: "first",
						CreatedAt: "2026-05-19T00:00:02Z", UpdatedAt: "2026-05-19T00:00:02Z",
					},
				},
			},
		},
	}
}

func TestSnapshotRecordsRoundTrip(t *testing.T) {
	snap := sampleSnapshot()
	records := SnapshotRecords(snap)
	if len(records) == 0 {
		t.Fatal("expected non-empty records")
	}
	for _, r := range records {
		if r.SchemaVersion != SchemaVersion {
			t.Fatalf("missing schema version on %s/%s", r.RecordType, r.ID)
		}
	}
	roundTrip, err := RecordsSnapshot(records)
	if err != nil {
		t.Fatalf("RecordsSnapshot: %v", err)
	}
	if len(roundTrip.Teams) != 1 || len(roundTrip.States) != 1 ||
		len(roundTrip.Users) != 1 || len(roundTrip.Labels) != 1 ||
		len(roundTrip.Projects) != 1 || len(roundTrip.Issues) != 1 {
		t.Fatalf("counts mismatch after round-trip: %+v", roundTrip)
	}
	if len(roundTrip.Issues[0].Comments) != 1 {
		t.Fatalf("comment lost: %+v", roundTrip.Issues[0])
	}
}

func TestDeterministicJSONL(t *testing.T) {
	snap := sampleSnapshot()
	a, _ := JSONLBytes(SnapshotRecords(snap))
	b, _ := JSONLBytes(SnapshotRecords(snap))
	if !bytes.Equal(a, b) {
		t.Fatal("JSONL output is not deterministic across calls")
	}
}

func TestEncryptedRoundTripWithAgeKey(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl.zst.age")
	records := SnapshotRecords(sampleSnapshot())
	if err := WriteEncryptedJSONL(path, identity.Recipient().String(), records); err != nil {
		t.Fatalf("WriteEncryptedJSONL: %v", err)
	}
	got, err := ReadEncryptedJSONL(path, identity.String())
	if err != nil {
		t.Fatalf("ReadEncryptedJSONL: %v", err)
	}
	if len(got) != len(records) {
		t.Fatalf("got %d records, want %d", len(got), len(records))
	}
}

func TestEncryptedRoundTripWithSSHKey(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pubLine := string(ssh.MarshalAuthorizedKey(sshPub))
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	privPEM := string(pem.EncodeToMemory(pemBlock))
	_, err = agessh.ParseIdentity([]byte(privPEM))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl.zst.age")
	records := SnapshotRecords(sampleSnapshot())
	if err := WriteEncryptedJSONL(path, pubLine, records); err != nil {
		t.Fatalf("WriteEncryptedJSONL: %v", err)
	}
	got, err := ReadEncryptedJSONL(path, privPEM)
	if err != nil {
		t.Fatalf("ReadEncryptedJSONL: %v", err)
	}
	if len(got) != len(records) {
		t.Fatalf("got %d records, want %d", len(got), len(records))
	}
}

func TestPriorityZeroSurvivesWire(t *testing.T) {
	// Linear's priority=0 means "No priority" — a real, common value.
	// Without omitempty the field must serialize as `"priority":0`; this
	// is the regression that lock the wire schema.
	records := SnapshotRecords(linear.Snapshot{
		Teams:  []linear.Team{{ID: "t1", Key: "LIN", Name: "L"}},
		States: []linear.WorkflowState{{ID: "s1", TeamID: "t1", Name: "B", Type: "backlog"}},
		Issues: []linear.Issue{
			{
				ID: "i1", Identifier: "LIN-1", Title: "T",
				TeamID: "t1", StateID: "s1", Priority: 0,
				CreatedAt: "2026-05-19T00:00:00Z", UpdatedAt: "2026-05-19T00:00:01Z",
			},
		},
	})
	raw, err := JSONLBytes(records)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"priority":0`)) {
		t.Fatalf("priority=0 was elided from the wire shape; output:\n%s", raw)
	}
	// Round-trip back: Priority must still be 0.
	got, err := RecordsSnapshot(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Issues) != 1 || got.Issues[0].Priority != 0 {
		t.Fatalf("priority round-trip: got %+v", got.Issues)
	}
}

func TestParseRecipientErrors(t *testing.T) {
	if _, err := ParseRecipient(""); err == nil {
		t.Error("empty should error")
	}
	if _, err := ParseRecipient("age1bogus"); err == nil {
		t.Error("bogus age recipient should error")
	}
	if _, err := ParseRecipient("ssh-rsa AAAA invalid"); err == nil {
		t.Error("bogus ssh recipient should error")
	}
	if _, err := ParseRecipient("plain-string"); err == nil {
		t.Error("unsupported format should error")
	}
}

func TestParseIdentitiesErrors(t *testing.T) {
	if _, err := ParseIdentities(""); err == nil {
		t.Error("empty should error")
	}
	if _, err := ParseIdentities("AGE-SECRET-KEY-1ZZZ"); err == nil {
		t.Error("bogus age identity should error")
	}
	if _, err := ParseIdentities("-----BEGIN OPENSSH PRIVATE KEY-----\nnotreal\n-----END OPENSSH PRIVATE KEY-----\n"); err == nil {
		t.Error("bogus ssh identity should error")
	}
}

func TestWriteEncryptedJSONLRecipientError(t *testing.T) {
	dir := t.TempDir()
	if err := WriteEncryptedJSONL(filepath.Join(dir, "x.age"), "not-a-recipient", nil); err == nil {
		t.Fatal("expected recipient parse failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "x.age")); err == nil {
		t.Fatal("file should not have been created on recipient failure")
	}
}

func TestReadEncryptedJSONLMissingFile(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	if _, err := ReadEncryptedJSONL("/nonexistent/path/here.age", id.String()); err == nil {
		t.Fatal("expected open error")
	}
}

func TestReadEncryptedJSONLWrongIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl.zst.age")
	idEnc, _ := age.GenerateX25519Identity()
	idDec, _ := age.GenerateX25519Identity()
	if err := WriteEncryptedJSONL(path, idEnc.Recipient().String(), SnapshotRecords(sampleSnapshot())); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEncryptedJSONL(path, idDec.String()); err == nil {
		t.Fatal("expected decryption failure with wrong identity")
	}
}

func TestWriteEncryptedRefusesOverwrite(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl.zst.age")
	records := SnapshotRecords(sampleSnapshot())
	if err := WriteEncryptedJSONL(path, identity.Recipient().String(), records); err != nil {
		t.Fatal(err)
	}
	if err := WriteEncryptedJSONL(path, identity.Recipient().String(), records); err == nil {
		t.Fatal("expected O_EXCL refusal on existing file")
	}
}
