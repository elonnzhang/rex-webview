package rexwebview

import "testing"

func TestAppAndRexShortcuts(t *testing.T) {
	keymap := []byte(`{"bindings":[
		{"keys":["cmd+s"],"action":"custom-save"},
		{"keys":["cmd+t","a"],"action":"custom-tab"},
		{"keys":["cmd+q","a"],"action":"quit-prefix"},
		{"keys":["cmd+l","cmd+w"],"action":"close-suffix"},
		{"keys":["cmd+j","p"],"action":"custom-jump"}
		,{"keys":["ctrl+h"],"action":"pane.resize.left","mode":"resize"}
	]}`)
	if hit, _ := matchKeys(parseBindings(keymap, nil), []string{"cmd+s"}); hit == nil || hit.Action != "custom-save" {
		t.Fatal("the SDK reserved an application-specific key by default")
	}
	reserved := []string{"cmd+s", "cmd+t"}
	bindings := parseBindings(keymap, reserved)
	if hit, more := matchKeys(bindings, []string{"ctrl+h"}); hit != nil || more {
		t.Fatal("a binding from Rex's inactive key mode was forwarded")
	}
	for _, key := range []string{"cmd+s", "cmd+t", "cmd+q", "cmd+l"} {
		if hit, more := matchKeys(bindings, []string{key}); hit != nil || more {
			t.Errorf("reserved key or unreachable prefix forwarded: %s", key)
		}
	}
	if hit, _ := matchKeys(bindings, []string{"cmd+d"}); hit == nil || hit.Action != "pane.split.right" {
		t.Fatal("unrelated Rex shortcut was removed")
	}
	f := &follower{reservedKeys: reserved, bindings: bindings}
	for _, keep := range []string{"cmd+s", "cmd+w", "cmd+q"} {
		if !f.key("cmd+j") || f.key(keep) || f.key("p") {
			t.Fatalf("%s must go to the app and clear a pending Rex sequence", keep)
		}
	}
}
