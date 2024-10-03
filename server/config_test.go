package server

import "testing"

func TestParseConfig(t *testing.T) {
	// TODO:
	cfg, err := ParseConfig("../config.toml")
	if err != nil {
		t.Errorf("parse config file failed:%s", err)
	}

	t.Errorf("%+v", cfg)
}
