package db_test

import (
	"testing"
	"time"

	"proxyness/server/internal/db"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// ---- Users ----

func TestCreateUser(t *testing.T) {
	d := openTestDB(t)
	u, err := d.CreateUser("alice")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if u.Name != "alice" {
		t.Errorf("expected name alice, got %q", u.Name)
	}
	if u.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestListUsers(t *testing.T) {
	d := openTestDB(t)

	u1, _ := d.CreateUser("alice")
	u2, _ := d.CreateUser("bob")

	// Add two devices to alice, one to bob
	d.CreateDevice(u1.ID, "phone")
	d.CreateDevice(u1.ID, "laptop")
	d.CreateDevice(u2.ID, "desktop")

	users, err := d.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}

	// Find alice
	var alice, bob db.User
	for _, u := range users {
		switch u.Name {
		case "alice":
			alice = u
		case "bob":
			bob = u
		}
	}
	if alice.DeviceCount != 2 {
		t.Errorf("alice: expected 2 devices, got %d", alice.DeviceCount)
	}
	if bob.DeviceCount != 1 {
		t.Errorf("bob: expected 1 device, got %d", bob.DeviceCount)
	}
}

func TestDeleteUser(t *testing.T) {
	d := openTestDB(t)

	u, _ := d.CreateUser("charlie")
	dev, _ := d.CreateDevice(u.ID, "phone")

	// Sanity: device exists
	devs, _ := d.ListDevices(u.ID)
	if len(devs) != 1 {
		t.Fatalf("expected 1 device before delete, got %d", len(devs))
	}

	if err := d.DeleteUser(u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// User is gone
	users, _ := d.ListUsers()
	for _, usr := range users {
		if usr.ID == u.ID {
			t.Error("user still present after delete")
		}
	}

	// Device cascaded — use GetDeviceByKey to confirm
	_, err := d.GetDeviceByKey(dev.Key)
	if err == nil {
		t.Error("expected error for deleted device's key, got nil")
	}
}

// ---- Devices ----

func TestCreateDevice(t *testing.T) {
	d := openTestDB(t)
	u, _ := d.CreateUser("dave")

	dev, err := d.CreateDevice(u.ID, "tablet")
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if dev.ID == 0 {
		t.Error("expected non-zero device ID")
	}
	if len(dev.Key) != 64 {
		t.Errorf("expected 64-char hex key, got length %d: %q", len(dev.Key), dev.Key)
	}
	if !dev.Active {
		t.Error("expected device to be active by default")
	}
}

func TestListDevices(t *testing.T) {
	d := openTestDB(t)
	u, _ := d.CreateUser("eve")

	d.CreateDevice(u.ID, "phone")
	d.CreateDevice(u.ID, "tablet")

	devs, err := d.ListDevices(u.ID)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devs))
	}
}

func TestSetDeviceActive(t *testing.T) {
	d := openTestDB(t)
	u, _ := d.CreateUser("frank")
	dev, _ := d.CreateDevice(u.ID, "phone")

	if err := d.SetDeviceActive(dev.ID, false); err != nil {
		t.Fatalf("SetDeviceActive: %v", err)
	}
	devs, _ := d.ListDevices(u.ID)
	if devs[0].Active {
		t.Error("expected device inactive after SetDeviceActive(false)")
	}

	if err := d.SetDeviceActive(dev.ID, true); err != nil {
		t.Fatalf("SetDeviceActive: %v", err)
	}
	devs, _ = d.ListDevices(u.ID)
	if !devs[0].Active {
		t.Error("expected device active after SetDeviceActive(true)")
	}
}

func TestGetActiveKeys(t *testing.T) {
	d := openTestDB(t)
	u, _ := d.CreateUser("grace")
	dev1, _ := d.CreateDevice(u.ID, "phone")
	dev2, _ := d.CreateDevice(u.ID, "tablet")

	// Deactivate dev2
	d.SetDeviceActive(dev2.ID, false)

	keys, err := d.GetActiveKeys()
	if err != nil {
		t.Fatalf("GetActiveKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 active key, got %d", len(keys))
	}
	if keys[0] != dev1.Key {
		t.Errorf("expected key %q, got %q", dev1.Key, keys[0])
	}
}

func TestGetDeviceByKey(t *testing.T) {
	d := openTestDB(t)
	u, _ := d.CreateUser("henry")
	dev, _ := d.CreateDevice(u.ID, "laptop")

	got, err := d.GetDeviceByKey(dev.Key)
	if err != nil {
		t.Fatalf("GetDeviceByKey: %v", err)
	}
	if got.ID != dev.ID {
		t.Errorf("expected device ID %d, got %d", dev.ID, got.ID)
	}
	if got.UserName != "henry" {
		t.Errorf("expected UserName henry, got %q", got.UserName)
	}

	// Non-existent key
	_, err = d.GetDeviceByKey("0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestDeleteDevice(t *testing.T) {
	d := openTestDB(t)
	u, _ := d.CreateUser("iris")
	dev, _ := d.CreateDevice(u.ID, "phone")

	if err := d.DeleteDevice(dev.ID); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	devs, _ := d.ListDevices(u.ID)
	if len(devs) != 0 {
		t.Errorf("expected 0 devices after delete, got %d", len(devs))
	}
}

// ---- Traffic ----

func TestRecordTraffic(t *testing.T) {
	d := openTestDB(t)
	u, _ := d.CreateUser("jack")
	dev, _ := d.CreateDevice(u.ID, "phone")

	hour := time.Now().Truncate(time.Hour)

	// First insert
	if err := d.RecordTraffic(dev.ID, hour, 100, 200, 5); err != nil {
		t.Fatalf("RecordTraffic: %v", err)
	}

	// UPSERT — should accumulate
	if err := d.RecordTraffic(dev.ID, hour, 50, 100, 2); err != nil {
		t.Fatalf("RecordTraffic upsert: %v", err)
	}

	stats, err := d.GetTraffic("day")
	if err != nil {
		t.Fatalf("GetTraffic: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 stat entry, got %d", len(stats))
	}
	s := stats[0]
	if s.BytesIn != 150 {
		t.Errorf("expected BytesIn 150, got %d", s.BytesIn)
	}
	if s.BytesOut != 300 {
		t.Errorf("expected BytesOut 300, got %d", s.BytesOut)
	}
	if s.Connections != 7 {
		t.Errorf("expected Connections 7, got %d", s.Connections)
	}
}

func TestGetOverview(t *testing.T) {
	d := openTestDB(t)
	u, _ := d.CreateUser("kate")
	dev1, _ := d.CreateDevice(u.ID, "phone")
	dev2, _ := d.CreateDevice(u.ID, "tablet")
	d.SetDeviceActive(dev2.ID, false)

	hour := time.Now().Truncate(time.Hour)
	d.RecordTraffic(dev1.ID, hour, 1000, 2000, 10)

	ov, err := d.GetOverview()
	if err != nil {
		t.Fatalf("GetOverview: %v", err)
	}
	if ov.TotalBytesIn != 1000 {
		t.Errorf("expected TotalBytesIn 1000, got %d", ov.TotalBytesIn)
	}
	if ov.TotalBytesOut != 2000 {
		t.Errorf("expected TotalBytesOut 2000, got %d", ov.TotalBytesOut)
	}
	// 1 active device (dev2 is deactivated)
	if ov.TotalDevices != 1 {
		t.Errorf("expected TotalDevices 1, got %d", ov.TotalDevices)
	}
}
