package main

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

func hasiruem() {
	hash, _ := bcrypt.GenerateFromPassword([]byte("ЛЮБОЙ НОВЫЙ ПАРОЛЬ"), 12)
	fmt.Println(string(hash))
}
