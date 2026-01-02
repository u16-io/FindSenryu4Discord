package db

import (
	"log"
	"os"

	"github.com/jinzhu/gorm"
	"github.com/u16-io/FindSenryu4Discord/model"

	// SQLite3 driver for Gorm
	_ "github.com/mattn/go-sqlite3"
)

var (
	// DB is GormDB
	DB  *gorm.DB
	err error
)

// Init is initialize dbs from main function
func Init() {
	_, err := os.Stat("data")
	if os.IsNotExist(err) {
		if err := os.Mkdir("data", 0777); err != nil {
			log.Fatal(err)
		}
	}
	initDB()
	initLedis()
}

func initDB() {
	DB, err = gorm.Open("sqlite3", "data/senryu.db")
	if err != nil {
		panic(err)
	}
	DB.AutoMigrate(&model.Senryu{})
}

// Close is closing db
func Close() {
	if err := DB.Close(); err != nil {
		panic(err)
	}
}
