package main

import "flag"

// The flags the run takes. The base URL is the origin the ceremonies sign
// over; the insecure flag is the self-signed development proxy's.
var (
	baseURLFlag  = flag.String("base-url", "http://localhost:3000", "the server's base URL")
	insecureFlag = flag.Bool("insecure", false, "skip the TLS verification - the self-signed development proxy")
)

func flagBaseURL() string {
	flag.Parse()
	return *baseURLFlag
}

func flagInsecure() bool { return *insecureFlag }
