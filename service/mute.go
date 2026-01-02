package service

import (
	"os"

	"github.com/u16-io/FindSenryu4Discord/db"
)

// IsMute is true if the channel is muted.
func IsMute(id string) bool {
	k, err := db.LDB.SIsMember([]byte("mute"), []byte(id))
	if err != nil {
		os.Exit(1)
	}
	if k == 0 {
		return false
	}
	return true
}

// ToMute is to mute.
func ToMute(id string) error {
	if _, err := db.LDB.SAdd([]byte("mute"), []byte(id)); err != nil {
		return err
	}
	return nil
}

// ToUnMute is to unmute.
func ToUnMute(id string) error {
	if _, err := db.LDB.SRem([]byte("mute"), []byte(id)); err != nil {
		return err
	}
	return nil
}
