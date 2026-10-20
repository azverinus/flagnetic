package id_test

import (
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

type (
	Project struct{}
	Flag    struct{}

	ProjectID = id.Of[Project]
	FlagID    = id.Of[Flag]
)

func deleteFlag(project ProjectID, flag FlagID) string {
	return fmt.Sprintf("delete %s from %s", flag, project)
}

// Identifiers are typed per entity, so passing one entity's identifier where
// another's is expected does not compile:
//
//	deleteFlag(flag, project) // cannot use flag (FlagID) as ProjectID
func ExampleOf() {
	src := id.NewDeterministicSource(clock.NewFake(time.UnixMilli(1469918264193)))
	project := id.New[Project](src)
	flag := id.New[Flag](src)

	fmt.Println(deleteFlag(project, flag))
	// Output: delete 01563df4-bb81-7000-8000-000000000001 from 01563df4-bb81-7000-8000-000000000000
}
