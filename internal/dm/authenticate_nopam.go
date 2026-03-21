//go:build nopam

package dm

// Authenticate validates credentials (shadow/crypt when built with tag nopam).
func Authenticate(conf *config, username, password string) (authHandle, error) {
	return AuthenticateNopam(conf, username, password)
}
