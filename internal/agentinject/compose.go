package agentinject

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"strings"
)

const mimeBoundary = "==PULUMI_UI_AGENT=="

// ComposeMultipart wraps two shell scripts into a multipart/mixed MIME
// message understood by cloud-init. Part 1 is the program's original
// cloud-init script; Part 2 is the agent bootstrap.
//
// If programScript is nil or empty, the result contains only the agent
// bootstrap as a single-part message (no MIME wrapping needed, but we
// still wrap for consistency so cloud-init always sees the same format).
func ComposeMultipart(programScript, agentScript []byte) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=\"%s\"\nMIME-Version: 1.0\n\n", mimeBoundary)

	if len(programScript) > 0 {
		fmt.Fprintf(&buf, "--%s\n", mimeBoundary)
		buf.WriteString("Content-Type: text/x-shellscript; charset=\"utf-8\"\n\n")
		buf.Write(programScript)
		if !bytes.HasSuffix(programScript, []byte("\n")) {
			buf.WriteByte('\n')
		}
	}

	fmt.Fprintf(&buf, "--%s\n", mimeBoundary)
	buf.WriteString("Content-Type: text/x-shellscript; charset=\"utf-8\"\n\n")
	buf.Write(agentScript)
	if !bytes.HasSuffix(agentScript, []byte("\n")) {
		buf.WriteByte('\n')
	}

	fmt.Fprintf(&buf, "--%s--\n", mimeBoundary)
	return buf.Bytes()
}

// GzipBase64 compresses data with gzip and returns the base64-encoded result.
// OCI instance metadata has a 32 KB total limit; gzip reduces the payload
// significantly. cloud-init detects gzip via magic bytes and decompresses.
func GzipBase64(data []byte) string {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write(data)
	gz.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// ComposeAndEncode composes two scripts via multipart MIME, then gzip+base64
// encodes the result. This is the main entry point for Go programs that need
// to produce a user_data value with agent bootstrap injection.
func ComposeAndEncode(programScript, agentScript []byte) string {
	composed := ComposeMultipart(programScript, agentScript)
	return GzipBase64(composed)
}

// DecodeUserData attempts to decode a base64+gzip user_data value back to
// the original script bytes. Returns the raw bytes and true on success,
// or nil and false if the value is not recognizable.
func DecodeUserData(encoded string) ([]byte, bool) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	// Check for gzip magic bytes (1f 8b)
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		r, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, false
		}
		var decompressed bytes.Buffer
		if _, err := decompressed.ReadFrom(r); err != nil {
			return nil, false
		}
		r.Close()
		return decompressed.Bytes(), true
	}
	return raw, true
}

// HasAgentBootstrap checks whether a raw (decoded) user_data script already
// contains the agent bootstrap marker, indicating it was previously composed.
func HasAgentBootstrap(rawScript []byte) bool {
	return strings.Contains(string(rawScript), AgentBootstrapMarker)
}
