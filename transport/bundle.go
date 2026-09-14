package transport

import (
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
)

func InventoryToPB(inv credential.Inventory) *connectorpb.GrantInventory {
	return &connectorpb.GrantInventory{}
}

func InventoryFromPB(pb *connectorpb.GrantInventory) credential.Inventory {
	return credential.Inventory{}
}

func BundleToPB(b credential.Bundle) *connectorpb.AcquireBundleResponse {
	return &connectorpb.AcquireBundleResponse{}
}

func BundleFromPB(pb *connectorpb.AcquireBundleResponse) credential.Bundle {
	return credential.Bundle{}
}
