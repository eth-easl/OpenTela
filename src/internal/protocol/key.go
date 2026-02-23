package protocol

import (
	"fmt"
	"opentela/internal/common"
	"os"
	"path"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/mitchellh/go-homedir"
)

func writeKeyToFile(priv crypto.PrivKey) {
	keyData, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		common.Logger.Error("Error while marshalling private key: ", err)
	}
	home, err := homedir.Dir()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	keyPath := path.Join(home, ".ocfcore", "keys", "id")
	err = os.MkdirAll(path.Dir(keyPath), os.ModePerm)
	if err != nil {
		common.Logger.Error("Could not create keys directory", "error", err)
		os.Exit(1)
	}
	err = os.WriteFile(keyPath, keyData, 0600)
	if err != nil {
		common.Logger.Error("Could not write key to file", err)
		os.Exit(1)
	}
}

func loadKeyFromFile() crypto.PrivKey {
	home, err := homedir.Dir()
	if err != nil {
		fmt.Println(err)
		return nil
	}
	keyPath := path.Join(home, ".ocfcore", "keys", "id")
	common.Logger.Info("Looking for keys under: ", keyPath)
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		common.Logger.Error("Error while reading private key file: ", err)
		return nil
	}
	priv, err := crypto.UnmarshalPrivateKey(keyData)
	if err != nil {
		common.Logger.Error("Error while unmarshalling private key: ", err)
		return nil
	}
	return priv
}
