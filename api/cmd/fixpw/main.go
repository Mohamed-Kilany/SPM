package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://spm:spm_local_dev_password@localhost:5432/spm?sslmode=disable"
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		log.Fatalf("DB connect: %v", err)
	}
	defer conn.Close(ctx)

	users := []struct {
		username string
		password string
	}{
		{"admin", "Admin@1234"},
		{"demo", "Demo@1234"},
		{"developer", "Demo@1234"},
		{"tester", "Demo@1234"},
	}

	for _, u := range users {
		hash, err := bcrypt.GenerateFromPassword([]byte(u.password), bcrypt.DefaultCost)
		if err != nil {
			log.Fatalf("bcrypt %s: %v", u.username, err)
		}
		_, err = conn.Exec(ctx,
			`UPDATE users SET password_hash = $1 WHERE username = $2`,
			string(hash), u.username,
		)
		if err != nil {
			log.Fatalf("update %s: %v", u.username, err)
		}
		fmt.Printf("✓ Updated %s  hash=%s\n", u.username, string(hash))
	}
	fmt.Println("Done — all passwords fixed.")
}
