package rest

// import (
// 	"fmt"
//
// 	mtypes "pkg.akt.dev/go/node/market/v1beta4"
// )
//
// const (
// 	deploymentPathPrefix = "/deployment/{dseq}"
// 	leasePathPrefix      = "/lease/{dseq}/{gseq}/{oseq}"
// 	hostnamePrefix       = "/hostname"
// 	endpointPrefix       = "/endpoint"
// 	migratePathPrefix    = "/migrate"
// )
//
// func versionPath() string {
// 	return "version"
// }
//
// func statusPath() string {
// 	return "status"
// }
//
// func validatePath() string {
// 	return "validate"
// }
//
// func leasePath(id mtypes.LeaseID) string {
// 	return fmt.Sprintf("lease/%d/%d/%d", id.DSeq, id.GSeq, id.OSeq)
// }
//
// func submitManifestPath(dseq uint64) string {
// 	return fmt.Sprintf("deployment/%d/manifest", dseq)
// }
//
// func getManifestPath(id mtypes.LeaseID) string {
// 	return fmt.Sprintf("%s/manifest", leasePath(id))
// }
//
// func leaseStatusPath(id mtypes.LeaseID) string {
// 	return fmt.Sprintf("%s/status", leasePath(id))
// }
//
// func leaseShellPath(lID mtypes.LeaseID) string {
// 	return fmt.Sprintf("%s/shell", leasePath(lID))
// }
//
// func leaseEventsPath(id mtypes.LeaseID) string {
// 	return fmt.Sprintf("%s/kubeevents", leasePath(id))
// }
//
// func serviceStatusPath(id mtypes.LeaseID, service string) string {
// 	return fmt.Sprintf("%s/service/%s/status", leasePath(id), service)
// }
//
// func serviceLogsPath(id mtypes.LeaseID) string {
// 	return fmt.Sprintf("%s/logs", leasePath(id))
// }
