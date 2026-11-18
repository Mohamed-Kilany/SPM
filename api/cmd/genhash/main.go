package main

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	for _, pw := range []string{"Admin@1234", "Demo@1234"} {
		h, _ := bcrypt.GenerateFromPassword([]byte(pw), 10)
		fmt.Printf("%s => %s\n", pw, string(h))
	}
}
