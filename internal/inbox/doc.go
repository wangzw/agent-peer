// Package inbox is the only package that touches Claude Code's internal formats: it looks up session records in the session registry and watches that directory, writes a signed message frame to a session's inbox socket, and provides name normalization and signature cleaning.
package inbox
