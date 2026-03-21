package dm

import "os"

// LoadConfig loads configuration from a properties file (empty path skips the file).
func LoadConfig(path string) *Config {
	return loadConfig(path)
}

// InitLogger configures process logging according to conf.
func InitLogger(conf *Config) {
	initLogger(conf)
}

// StartDaemon switches stdin/stdout/stderr to the configured TTY when in daemon mode.
func StartDaemon(conf *Config) *os.File {
	return startDaemon(conf)
}

// StopDaemon restores the terminal after daemon mode.
func StopDaemon(conf *Config, fTTY *os.File) {
	stopDaemon(conf, fTTY)
}
