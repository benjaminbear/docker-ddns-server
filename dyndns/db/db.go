package db

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

const (
	dbDir  = "database"
	dbFile = "ddns.db"
)

// InitDB creates an empty database and creates all tables if there isn't already one, or opens the existing one.
func InitDB() (*gorm.DB, error) {
	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		err = os.MkdirAll(dbDir, os.ModePerm)
		if err != nil {
			return nil, err
		}
	}

	db, err := gorm.Open(sqlite.Open(filepath.Join(dbDir, dbFile)))
	if err != nil {
		return db, err
	}

	path, _ := filepath.Abs(filepath.Join(dbDir, dbFile))
	fmt.Println("Database path:", path)

	err = db.AutoMigrate(&Host{}, &CName{}, &Log{})

	return db, err
}
