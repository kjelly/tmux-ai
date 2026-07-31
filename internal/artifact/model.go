// Package artifact owns runner-validated handoff files.  It deliberately
// exposes opaque IDs rather than paths to workflow policy and coordinators.
package artifact

// ReceiptFileName is the runner-owned control file an agent may write in its
// outbox instead of rendering the status receipt on screen. It belongs to the
// control plane, so Collect never turns it into a forwardable artifact.
const ReceiptFileName = "_handoff_receipt.json"

type Limits struct {
	MaxFilesPerTurn int
	MaxFileBytes    int64
	MaxTurnBytes    int64
	MaxRunFiles     int
	MaxRunBytes     int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxFilesPerTurn: 8,
		MaxFileBytes:    1 << 20,
		MaxTurnBytes:    4 << 20,
		MaxRunFiles:     64,
		MaxRunBytes:     32 << 20,
	}
}

type Claim struct {
	RelPath string `json:"relpath"`
	Kind    string `json:"kind,omitempty"`
	Title   string `json:"title,omitempty"`
}

type Ref struct {
	ID           string `json:"id"`
	ProducerID   string `json:"producer_id"`
	Round        int    `json:"round"`
	OriginalName string `json:"original_name"`
	Kind         string `json:"kind,omitempty"`
	Title        string `json:"title,omitempty"`
	MediaType    string `json:"media_type"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	ObjectName   string `json:"object_name"`
	RuntimePath  string `json:"-"`
}

type Manifest struct {
	Version   int    `json:"version"`
	RunID     string `json:"run_id"`
	Artifacts []Ref  `json:"artifacts"`
}

type Store interface {
	NewOutbox(memberID string, round int) (string, error)
	Collect(memberID string, round int, outbox string, claims []Claim) ([]Ref, error)
	Resolve(ids []string) ([]Ref, error)
	Verify(refs []Ref) error
	Manifest() Manifest
	Root() string
	Close() error
}
