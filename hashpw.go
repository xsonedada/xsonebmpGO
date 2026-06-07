//go:build ignore

package main

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	hash, _ := bcrypt.GenerateFromPassword([]byte("YOUR_PASSWORD_HERE"), 12)
	fmt.Println(string(hash))
}
