package port

import "testing"

func TestMutationActorKeepsMachineDistinctFromAdministrativeID(t *testing.T) {
	admin, err := AdminMutationActor(42)
	if err != nil || !admin.Valid() || admin.Reference != "admin:42" || admin.StaffID != 42 {
		t.Fatalf("admin=%+v err=%v", admin, err)
	}
	machine, err := MachineMutationActor("external-audience")
	if err != nil || !machine.Valid() || machine.Reference != "machine:external-audience" || machine.StaffID != 0 {
		t.Fatalf("machine=%+v err=%v", machine, err)
	}
	if (MutationActor{Kind: MutationActorMachine, Reference: "machine:external-audience", StaffID: 42}).Valid() {
		t.Fatal("machine actor accepted a staff/admin ID")
	}
}

func TestMachineMutationActorRejectsUnsafeClientReferences(t *testing.T) {
	for _, clientID := range []string{"", " client", "client ", "admin:7", "client\n7"} {
		if _, err := MachineMutationActor(clientID); err == nil {
			t.Fatalf("client ID %q accepted", clientID)
		}
	}
}
