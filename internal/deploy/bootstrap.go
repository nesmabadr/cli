package deploy

import (
	"context"
	"encoding/json"
	"regexp"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrlClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/kio"

	"github.com/kyma-project/cli/internal/kube"
	"github.com/kyma-project/cli/internal/kustomize"
)

const (
	wildCardRoleAndAssignment = `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kyma-cli-provisioned-wildcard
rules:
- apiGroups: ["*"]
  resources: ["*"]
  verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: lifecycle-manager-wildcard
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kyma-cli-provisioned-wildcard
subjects:
- kind: ServiceAccount
  name: lifecycle-manager-controller-manager
  namespace: kcp-system`
)

type patchStringValue struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
}

// Bootstrap deploys the kustomization files for the prerequisites for Kyma.
// Returns true if the Kyma CRD was deployed.
func Bootstrap(
	ctx context.Context, kustomizations []string, k8s kube.KymaKube, filters []kio.Filter, addWildCard, force bool,
	dryRun bool, isInKcpMode bool,
) (bool, error) {
	var defs []kustomize.Definition
	// defaults
	for _, k := range kustomizations {
		parsed, err := kustomize.ParseKustomization(k)
		if err != nil {
			return false, err
		}
		defs = append(defs, parsed)
	}

	// build manifests
	manifests, err := kustomize.BuildMany(defs, filters)
	if err != nil {
		return false, err
	}

	if addWildCard {
		manifests = append(manifests, []byte(wildCardRoleAndAssignment)...)
	}

	manifestObjs, err := applyManifests(
		ctx, k8s, manifests, applyOpts{
			dryRun, force, defaultRetries, defaultInitialBackoff},
	)
	if err != nil {
		return false, err
	}

	if _, err = PatchDeploymentWithInKcpModeFlag(ctx, k8s, manifestObjs, isInKcpMode); err != nil {
		return false, err
	}

	return hasKyma(string(manifests))
}

func PatchDeploymentWithInKcpModeFlag(ctx context.Context, k8s kube.KymaKube, manifestObjs []ctrlClient.Object, isInKcpMode bool) (*appsv1.Deployment, error) {
	var patchedDeployment *appsv1.Deployment
	for _, manifest := range manifestObjs {
		manifestJSON, err := getManifestJsonForDeployment(manifest)
		if err == nil && manifestJSON != nil {
			deploymentArgs, err := getDeploymentArgs(manifestJSON)
			if err == nil && deploymentArgs != nil {
				hasKcpFlag := checkDeploymentHasKcpFlag(deploymentArgs)
				if !hasKcpFlag && isInKcpMode {
					payload := []patchStringValue{{
						Op:    "add",
						Path:  "/spec/template/spec/containers/0/args/-",
						Value: "--in-kcp-mode",
					}}
					payloadBytes, _ := json.Marshal(payload)
					var err error
					if patchedDeployment, err = k8s.Static().AppsV1().Deployments(manifest.GetNamespace()).
						Patch(ctx, manifest.GetName(), types.JSONPatchType, payloadBytes, v1.PatchOptions{}); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return patchedDeployment, nil
}

func getManifestJsonForDeployment(manifest ctrlClient.Object) ([]byte, error) {
	if manifest.GetObjectKind().GroupVersionKind().Kind == "Deployment" {
		if manifestObj, success := manifest.(*unstructured.Unstructured); success {
			if manifestJSON, err := json.Marshal(manifestObj.Object); err != nil {
				return nil, err
			} else {
				return manifestJSON, nil
			}
		}
	}
	return nil, nil
}

func getDeploymentArgs(manifestJSON []byte) ([]string, error) {
	deployment := &appsv1.Deployment{}
	if err := json.Unmarshal(manifestJSON, deployment); err != nil {
		return nil, err
	}

	return deployment.Spec.Template.Spec.Containers[0].Args, nil
}

func checkDeploymentHasKcpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--in-kcp-mode" || arg == "--in-kcp-mode=true" {
			return true
		}
	}

	return false
}

func checkDeploymentReadiness(objs []ctrlClient.Object, k8s kube.KymaKube) error {
	for _, obj := range objs {
		if obj.GetObjectKind().GroupVersionKind().Kind != "Deployment" {
			continue
		}
		if err := k8s.WaitDeploymentStatus(
			obj.GetNamespace(), obj.GetName(), appsv1.DeploymentAvailable, corev1.ConditionTrue,
		); err != nil {
			return err
		}
	}
	return nil
}

// hasKyma checks if the given manifest contains the Kyma CRD
func hasKyma(manifest string) (bool, error) {
	r, err := regexp.Compile(`(names:)(?:[.\s\S]*)(kind: Kyma)(?:[.\s\S]*)(plural: kymas)`)
	if err != nil {
		return false, err
	}
	return r.MatchString(manifest), nil
}
