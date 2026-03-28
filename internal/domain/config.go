package domain

const (
	DefaultStorePath = "~/Library/Mobile Documents/com~apple~CloudDocs/VeilStore"
	DefaultTTL       = "24h"
)

func DefaultConfigTOML() string {
	return `version = 1
store_path = "` + DefaultStorePath + `"
default_ttl = "` + DefaultTTL + `"
`
}
