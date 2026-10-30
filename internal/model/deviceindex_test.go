package model

import (
	"fmt"
	"math/rand"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
)

// DeviceIndex answers exactly what FindDevice answers, for any slice order,
// repeated generations and duplicated device names.
func TestDeviceIndexMatchesFindDevice(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var list []resourcev1.ResourceSlice
	for i := range 40 {
		var devs []string
		for range 1 + rng.Intn(4) {
			devs = append(devs, fmt.Sprintf("dev-%d", rng.Intn(6)))
		}
		s := gslice(fmt.Sprintf("s%d", i), fmt.Sprintf("pool-%d", rng.Intn(3)), int64(rng.Intn(4)), 1, devs...)
		s.Spec.Driver = fmt.Sprintf("d%d.example.com", rng.Intn(2))
		for j := range s.Spec.Devices {
			s.Spec.Devices[j].Attributes = map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{"i": {IntValue: ptrInt(int64(i*10 + j))}}
		}
		list = append(list, s)
	}
	idx := NewDeviceIndex(list)
	for d := range 2 {
		for p := range 4 {
			for v := range 7 {
				driver, pool, name := fmt.Sprintf("d%d.example.com", d), fmt.Sprintf("pool-%d", p), fmt.Sprintf("dev-%d", v)
				wd, ws, wok := FindDevice(list, driver, pool, name)
				gd, gs, gok := idx.Find(driver, pool, name)
				if wok != gok || (wok && (ws.Name != gs.Name || *wd.Attributes["i"].IntValue != *gd.Attributes["i"].IntValue)) {
					t.Errorf("%s/%s/%s: FindDevice (%v, %v) != index (%v, %v)", driver, pool, name, wok, ws, gok, gs)
				}
			}
		}
	}
}

func ptrInt(v int64) *int64 { return &v }
