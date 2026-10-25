//go:build cgo

package auth

import (
	"errors"

	"github.com/msteinert/pam"
)

// PAM authenticates against the Pi's accounts through the named PAM service.
type PAM struct {
	Service string
}

// Authenticate runs the PAM auth and account stacks. The conversation answers the password
// prompt only; any other prompt fails the login.
func (p PAM) Authenticate(user, password string) error {
	t, err := pam.StartFunc(p.Service, user, func(style pam.Style, msg string) (string, error) {
		if style == pam.PromptEchoOff {
			return password, nil
		}
		if style == pam.ErrorMsg || style == pam.TextInfo {
			return "", nil
		}
		return "", errors.New("unexpected PAM prompt")
	})
	if err != nil {
		return err
	}
	if err := t.Authenticate(0); err != nil {
		return err
	}
	return t.AcctMgmt(0)
}
