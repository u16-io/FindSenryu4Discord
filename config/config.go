package config

import (
	"log"

	"github.com/BurntSushi/toml"
)

var conf *Config

// Config 全ての設定を格納
type Config struct {
	Discord struct {
		Token    string `toml:"Token"`
		Playing  string `toml:"Playing"`
		ClientID string `toml:"ClientID"`
	} `toml:"Discord"`
}

func init() {
	if _, err := toml.DecodeFile("config.toml", &conf); err != nil {
		log.Fatal(err)
	}
}

// GetConf is return config
func GetConf() *Config {
	return conf
}
