// Package buildcaps reports which optional modules were compiled into the binary.
// Prefer these runtime booleans to skip capability-dependent tests and scenarios;
// keep build tags only where code would not compile without the optional module.
package buildcaps

var (
	SQLite      = false
	SQLiteFTS5  = false
	PostgreSQL  = false
	MongoDB     = false
	Redis       = false
	Infinispan  = false
	Qdrant      = false
	S3          = false
	AWSKMS      = false
	Vault       = false
	OpenAI      = false
	MCP         = false
	TCPListener = false
	UDSListener = false
)
