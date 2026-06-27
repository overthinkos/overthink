package main

import (
	"testing"

	dbus "github.com/godbus/dbus/v5"
)

// TestIsCollectionUnlockedSignal verifies the DBus signal filter the in-core
// encrypted-volume unlock-waiter (enc.go) uses to wake only on a collection
// transitioning to unlocked. The Secret Service store-CRUD client moved to
// candy/plugin-secrets, but this one signal helper stays in core (keyring_unlock_signal.go).
func TestIsCollectionUnlockedSignal(t *testing.T) {
	tests := []struct {
		name string
		sig  *dbus.Signal
		want bool
	}{
		{
			name: "correct_unlock_signal",
			sig: &dbus.Signal{
				Name: "org.freedesktop.DBus.Properties.PropertiesChanged",
				Body: []any{
					"org.freedesktop.Secret.Collection",
					map[string]dbus.Variant{"Locked": dbus.MakeVariant(false)},
					[]string{},
				},
			},
			want: true,
		},
		{
			name: "locked_true_still_locked",
			sig: &dbus.Signal{
				Name: "org.freedesktop.DBus.Properties.PropertiesChanged",
				Body: []any{
					"org.freedesktop.Secret.Collection",
					map[string]dbus.Variant{"Locked": dbus.MakeVariant(true)},
					[]string{},
				},
			},
			want: false,
		},
		{
			name: "wrong_interface",
			sig: &dbus.Signal{
				Name: "org.freedesktop.DBus.Properties.PropertiesChanged",
				Body: []any{
					"org.freedesktop.Secret.Item",
					map[string]dbus.Variant{"Locked": dbus.MakeVariant(false)},
					[]string{},
				},
			},
			want: false,
		},
		{
			name: "unrelated_property",
			sig: &dbus.Signal{
				Name: "org.freedesktop.DBus.Properties.PropertiesChanged",
				Body: []any{
					"org.freedesktop.Secret.Collection",
					map[string]dbus.Variant{"Label": dbus.MakeVariant("foo")},
					[]string{},
				},
			},
			want: false,
		},
		{
			name: "nil_signal",
			sig:  nil,
			want: false,
		},
		{
			name: "wrong_signal_name",
			sig: &dbus.Signal{
				Name: "org.freedesktop.Secret.Service.CollectionCreated",
				Body: []any{"something"},
			},
			want: false,
		},
		{
			name: "empty_body",
			sig: &dbus.Signal{
				Name: "org.freedesktop.DBus.Properties.PropertiesChanged",
				Body: []any{},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCollectionUnlockedSignal(tt.sig)
			if got != tt.want {
				t.Errorf("isCollectionUnlockedSignal() = %v, want %v", got, tt.want)
			}
		})
	}
}
