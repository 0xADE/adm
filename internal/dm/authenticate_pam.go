//go:build !nopam

package dm

// Authenticate validates credentials (PAM when built with PAM support).
func Authenticate(conf *config, username, password string) (authHandle, error) {
	return AuthenticatePAM(conf, username, password)
}
