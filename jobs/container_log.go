package jobs

import (
	"github.com/smarterclayton/geard/containers"
	"github.com/smarterclayton/geard/systemd"
	"log"
	"os"
	"time"
)

type ContainerLogRequest struct {
	Id containers.Identifier
}

func (j *ContainerLogRequest) Execute(resp JobResponse) {
	if _, err := os.Stat(j.Id.UnitPathFor()); err != nil {
		resp.Failure(ErrContainerNotFound)
		return
	}

	w := resp.SuccessWithWrite(JobResponseOk, true, false)
	err := systemd.WriteLogsTo(w, j.Id.UnitNameFor(), 30, time.After(30*time.Second))
	if err != nil {
		log.Printf("job_container_log: Unable to fetch journal logs: %s\n", err.Error())
	}
}
