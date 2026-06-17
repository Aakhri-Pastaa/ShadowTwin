// Package transport will hold the host agent's mTLS shipper, which sends buffered
// events to the platform over a mutually authenticated TLS connection. The agent
// only ever sends; it exposes no inbound control channel. Not implemented in this
// slice — see CLAUDE.md for the phasing.
package transport
