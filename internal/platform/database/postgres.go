package database

import "fmt"

type Postgres struct { URL string }

func Connect(databaseURL string) (*Postgres, error) {
	if databaseURL == "" { return nil, fmt.Errorf("DATABASE_URL is required") }
	return &Postgres{URL: databaseURL}, nil
}
