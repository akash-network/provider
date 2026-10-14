// Package entityids is the fixture of TestEntityIDAuditFixture. Every line the
// audit must report carries a `// want` comment quoting part of the violation.
package entityids

import (
	"errors"

	"cosmossdk.io/log"

	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
)

type holder struct {
	log log.Logger
}

func accepted(l log.Logger, lid mtypes.LeaseID, plid *mtypes.LeaseID, did dtypes.DeploymentID) {
	// an ID in scope does not have to be logged
	l.Error("failed to convert lease ID", "err", errors.New("invalid"))

	l.Info("lease", "lid", lid)
	l.Info("lease pointer", "lid", plid)
	l.Info("derived order", "oid", lid.OrderID())
	l.With("did", did).Info("deployment via With")

	// only ID keys are checked for duplicates
	hl := l.With("hostname", "example.com")
	hl.Info("hostname twice", "hostname", "example.com")
}

func rejected(l log.Logger, lid mtypes.LeaseID, oid mtypes.OrderID, gid dtypes.GroupID, did dtypes.DeploymentID) {
	l.Info("ID under a non-ID key", "id", lid)            // want `key "id": LeaseID value must use key "lid"`
	l.Info("ID under another ID key", "oid", lid)         // want `key "oid": LeaseID value must use key "lid"`
	l.Info("string under an ID key", "lid", lid.String()) // want `key "lid": must carry a LeaseID, got string`
	l.With("gid", oid).Info("mismatch in With")           // want `key "gid": OrderID value must use key "oid"`

	h := holder{log: l.With("lid", lid)}
	h.log.Info("lease twice via field", "lid", lid) // want `key "lid": already bound by the logger's With`

	dl := l.With("did", did)
	dl.Info("deployment twice via variable", "did", did) // want `key "did": already bound by the logger's With`

	l.With("gid", gid).With("gid", gid).Info("group twice in a chain") // want `key "gid": already bound by the logger's With`
}
