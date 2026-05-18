package archive

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
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
					{ID: "c1", IssueID: "i1", AuthorID: "u1", Body: "first",
						CreatedAt: "2026-05-19T00:00:02Z", UpdatedAt: "2026-05-19T00:00:02Z"},
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
