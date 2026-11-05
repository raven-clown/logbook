package admin_test

import (
	"errors"

	"github.com/raven-clown/logbook/memory"
)

func asError(err error, target **memory.Error) bool {
	return errors.As(err, target)
}
