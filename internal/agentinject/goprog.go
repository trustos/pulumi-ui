package agentinject

// CfgKeyAgentBootstrap is the config key the engine uses to pass the
// rendered agent bootstrap script to Go programs. Go programs read this
// from their cfg map and pass it to buildCloudInit (or ComposeAndEncode
// directly) to produce a composed user_data value.
//
// The value is the raw rendered script (not base64), ready for multipart
// MIME composition.
const CfgKeyAgentBootstrap = "__agentBootstrap"
