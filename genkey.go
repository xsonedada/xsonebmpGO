//go:build ignore

package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func main() {
	b := make([]byte, 32)
	rand.Read(b)
	fmt.Println("SESSION_SECRET=" + hex.EncodeToString(b))
}
