// Package crypto is billing's name for go-common's field encryption
// (gopkg.aoctech.app/api-commons/fieldcrypt), which holds the one implementation
// and its tests. Nothing is implemented here: the aliases keep the call sites
// as they were.
package crypto

import "gopkg.aoctech.app/api-commons/fieldcrypt"

type Sealer = fieldcrypt.Sealer

var (
	NewSealer = fieldcrypt.NewSealer
	IsSealed  = fieldcrypt.IsSealed

	ErrNotSealed         = fieldcrypt.ErrNotSealed
	ErrNoKey             = fieldcrypt.ErrNoKey
	ErrUnknownGeneration = fieldcrypt.ErrUnknownGeneration
)
