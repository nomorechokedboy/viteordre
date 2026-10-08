package db

import (
	"database/sql"
	"fmt"
	"log"

	"encore.app/internal/config"
	"github.com/pocketbase/dbx"
)

func New(cfg *config.DatabaseConfig) (*dbx.DB, error) {
	tursoDB, err := sql.Open("turso", fmt.Sprintf("%s.db", cfg.Name))
	if err != nil {
		return nil, err
	}
	defer tursoDB.Close()

	db := dbx.NewFromDB(tursoDB, "sqlite")
	db.LogFunc = log.Printf

	return db, nil
}
