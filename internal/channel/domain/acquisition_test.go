package domain

import "testing"

func TestStateResolutionNeverCarriesAnAssetForNonUniqueState(t *testing.T) {
	for _, resolution := range []StateResolution{
		{Status: StateUnmatched},
		{Status: StateAmbiguous},
		{Status: StateAttributed, Asset: AcquisitionAsset{ChannelID: 9, Kind: "qrcode", AssetVersion: 2}},
	} {
		if !resolution.Valid() {
			t.Fatalf("resolution %+v should be valid", resolution)
		}
	}
	if (StateResolution{Status: StateAmbiguous, Asset: AcquisitionAsset{ChannelID: 9, Kind: "qrcode", AssetVersion: 2}}).Valid() {
		t.Fatal("ambiguous state must not expose a selected asset")
	}
}
