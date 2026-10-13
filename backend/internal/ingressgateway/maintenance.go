package ingressgateway

import (
	"net/http"
	"os"
)

const maintenanceFile = "/var/lib/guangyue-maintenance/pending"

func maintenancePending() bool {
	_, err := os.Lstat(maintenanceFile)
	return !os.IsNotExist(err)
}

func allowDuringMaintenance(method, path string) bool {
	return method == http.MethodGet && path == "/api/health"
}
