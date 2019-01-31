package test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/logger"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/gruntwork-io/terratest/modules/shell"
	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/gruntwork-io/terratest/modules/test-structure"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/stretchr/testify/require"
)

func TestK8STiller(t *testing.T) {
	t.Parallel()

	// Uncomment any of the following to skip that section during the test
	// os.Setenv("SKIP_create_test_copy_of_examples", "true")
	// os.Setenv("SKIP_create_test_service_account", "true")
	// os.Setenv("SKIP_create_terratest_options", "true")
	// os.Setenv("SKIP_terraform_apply", "true")
	// os.Setenv("SKIP_validate", "true")
	// os.Setenv("SKIP_cleanup", "true")

	// Create a directory path that won't conflict
	workingDir := filepath.Join(".", "stages", t.Name())

	test_structure.RunTestStage(t, "create_test_copy_of_examples", func() {
		testFolder := test_structure.CopyTerraformFolderToTemp(t, "..", "examples")
		logger.Logf(t, "path to test folder %s\n", testFolder)
		k8sTillerTerraformModulePath := filepath.Join(testFolder, "k8s-tiller")
		test_structure.SaveString(t, workingDir, "k8sTillerTerraformModulePath", k8sTillerTerraformModulePath)
	})

	// Create a ServiceAccount in its own namespace that we can use to login as for testing purposes.
	test_structure.RunTestStage(t, "create_test_service_account", func() {
		uniqueID := random.UniqueId()
		testServiceAccountName := fmt.Sprintf("%s-test-account", strings.ToLower(uniqueID))
		testServiceAccountNamespace := fmt.Sprintf("%s-test-account-namespace", strings.ToLower(uniqueID))
		tmpConfigPath := k8s.CopyHomeKubeConfigToTemp(t)
		kubectlOptions := k8s.NewKubectlOptions("", tmpConfigPath)

		k8s.CreateNamespace(t, kubectlOptions, testServiceAccountNamespace)
		kubectlOptions.Namespace = testServiceAccountNamespace
		k8s.CreateServiceAccount(t, kubectlOptions, testServiceAccountName)
		token := k8s.GetServiceAccountAuthToken(t, kubectlOptions, testServiceAccountName)
		err := k8s.AddConfigContextForServiceAccountE(t, kubectlOptions, testServiceAccountName, testServiceAccountName, token)
		// We do the error check and namespace deletion manually here, because we can't defer it within the test stage.
		if err != nil {
			k8s.DeleteNamespace(t, kubectlOptions, testServiceAccountNamespace)
			t.Fatal(err)
		}

		test_structure.SaveString(t, workingDir, "uniqueID", uniqueID)
		test_structure.SaveString(t, workingDir, "tmpKubectlConfigPath", tmpConfigPath)
		test_structure.SaveString(t, workingDir, "testServiceAccountName", testServiceAccountName)
		test_structure.SaveString(t, workingDir, "testServiceAccountNamespace", testServiceAccountNamespace)
	})

	test_structure.RunTestStage(t, "create_terratest_options", func() {
		uniqueID := test_structure.LoadString(t, workingDir, "uniqueID")
		testServiceAccountName := test_structure.LoadString(t, workingDir, "testServiceAccountName")
		testServiceAccountNamespace := test_structure.LoadString(t, workingDir, "testServiceAccountNamespace")

		k8sTillerTerraformModulePath := test_structure.LoadString(t, workingDir, "k8sTillerTerraformModulePath")
		k8sTillerTerratestOptions := createExampleK8STillerTerraformOptions(t, k8sTillerTerraformModulePath, uniqueID, testServiceAccountName, testServiceAccountNamespace)

		test_structure.SaveTerraformOptions(t, workingDir, k8sTillerTerratestOptions)
	})

	defer test_structure.RunTestStage(t, "cleanup", func() {
		k8sNamespaceTerratestOptions := test_structure.LoadTerraformOptions(t, workingDir)
		terraform.Destroy(t, k8sNamespaceTerratestOptions)

		testServiceAccountNamespace := test_structure.LoadString(t, workingDir, "testServiceAccountNamespace")
		kubectlOptions := k8s.NewKubectlOptions("", "")
		k8s.DeleteNamespace(t, kubectlOptions, testServiceAccountNamespace)
	})

	test_structure.RunTestStage(t, "terraform_apply", func() {
		k8sNamespaceTerratestOptions := test_structure.LoadTerraformOptions(t, workingDir)
		terraform.InitAndApply(t, k8sNamespaceTerratestOptions)
	})

	test_structure.RunTestStage(t, "validate", func() {
		k8sNamespaceTerratestOptions := test_structure.LoadTerraformOptions(t, workingDir)
		resourceNamespace := k8sNamespaceTerratestOptions.Vars["resource_namespace"].(string)
		tmpConfigPath := test_structure.LoadString(t, workingDir, "tmpKubectlConfigPath")
		testServiceAccountName := test_structure.LoadString(t, workingDir, "testServiceAccountName")
		kubectlOptions := k8s.NewKubectlOptions(testServiceAccountName, tmpConfigPath)
		kubectlOptions.Namespace = resourceNamespace

		runHelm(
			t,
			kubectlOptions,
			getHelmHome(t),
			"install",
			"stable/kubernetes-dashboard",
			"--wait",
		)
	})
}

func runHelm(t *testing.T, options *k8s.KubectlOptions, helmHome string, args ...string) {
	helmArgs := []string{"helm"}
	if options.ContextName != "" {
		helmArgs = append(helmArgs, "--kube-context", options.ContextName)
	}
	if options.ConfigPath != "" {
		helmArgs = append(helmArgs, "--kubeconfig", options.ConfigPath)
	}
	if options.Namespace != "" {
		helmArgs = append(helmArgs, "--namespace", options.Namespace)
	}
	helmArgs = append(helmArgs, args...)
	helmCmd := strings.Join(helmArgs, " ")

	// TODO: make this test platform independent
	helmEnvPath := filepath.Join(helmHome, "env")
	cmd := shell.Command{
		Command: "sh",
		Args: []string{
			"-c",
			fmt.Sprintf(". %s && %s", helmEnvPath, helmCmd),
		},
	}
	shell.RunCommand(t, cmd)
}

func getHelmHome(t *testing.T) string {
	home, err := homedir.Dir()
	require.NoError(t, err)
	helmHome := filepath.Join(home, ".helm")
	return helmHome
}