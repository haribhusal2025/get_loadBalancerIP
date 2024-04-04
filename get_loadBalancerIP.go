package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/olekukonko/tablewriter"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Spinner animation characters
var (
	chars = []string{"|", "/", "-", "\\"}
)

func main() {
	// Get current user
	currentUser, err := user.Current()
	if err != nil {
		fmt.Printf("Error getting current user: %v\n", err)
		os.Exit(1)
	}

	// Path to the kubeconfig file
	var kubeconfig string
	flag.StringVar(&kubeconfig, "kubeconfig", filepath.Join(currentUser.HomeDir, ".kube", "config"), "path to the kubeconfig file")
	flag.Parse()

	// Load kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Printf("Error loading kubeconfig: %v\n", err)
		os.Exit(1)
	}

	// Create Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("Error creating Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	// Print welcome message
	printWelcomeMessage(currentUser)

	// Prompt user for Ansible username
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("\nEnter the Ansible username to run ARP command (Ex: johndoe or johndoe-adm): ")
	ansibleUsername, _ := reader.ReadString('\n')
	ansibleUsername = strings.TrimSpace(ansibleUsername)

	// Get interface name starting with '7' using Ansible
	arpInterface := getInterfaceNameStartingWithSeven()
	if arpInterface == "" {
		fmt.Println("Failed to retrieve network interface starting with '7'. Please check your setup.")
		os.Exit(1)
	}

	// Prompt user for LB IPs
	fmt.Print("\nDo you want to get all LoadBalancer IPs ? (yes/no): ")
	option, _ := reader.ReadString('\n')
	option = strings.TrimSpace(option)

	// Get LB IPs based on user's choice
	var lbIPs []string
	if option == "yes" {
		lbIPs = getLoadBalancerIPsStartingWithSeven(clientset)
	} else if option == "no" {
		lbIPs = getSpecificLoadBalancerIPs(reader)
	} else {
		fmt.Println("Invalid option. Please choose 'yes' or 'no'.")
		os.Exit(1)
	}

	// Get all nodes in the cluster
	nodes, err := getAllNodes(clientset)
	if err != nil {
		fmt.Printf("Error fetching nodes: %v\n", err)
		os.Exit(1)
	}

	// Create inventory file
	err = createInventoryFile(nodes, ansibleUsername)
	if err != nil {
		fmt.Printf("Error creating inventory file: %v\n", err)
		os.Exit(1)
	}

	// Run ARP command on all nodes
	stopSpinner := loadingAnimation()
	defer stopSpinner() // Ensure spinner stops at the end
	runARPCommandOnAllNodes(nodes, arpInterface, lbIPs, ansibleUsername)

	// Remove the inventory file after displaying the final output
	err = removeInventoryFile()
	if err != nil {
		fmt.Printf("Error removing inventory file: %v\n", err)
	}
}

func getInterfaceNameStartingWithSeven() string {
	// Run a command using Ansible to get the interface name whose IP starts with '7'
	cmd := exec.Command("ansible", "-i", "k8s.inventory", "k8s[1]", "-m", "shell", "-a", "ip addr show | grep -oP '^\\d+: \\K[^:]*' | xargs -I{} ip addr show {} | grep -oP '(?<=inet\\s)7\\S+'")
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Error executing Ansible command: %v\n", err)
		return "" // Return empty string or handle error appropriately
	}
	interfaceName := strings.TrimSpace(string(out))
	return interfaceName
}

func printWelcomeMessage(currentUser *user.User) {
	fmt.Println("\n*******************************************")
	fmt.Printf("*** Welcome, %s! ***\n", currentUser.Username)
	fmt.Println("*******************************************")
	fmt.Printf("\x1b[3mThis tool helps you find the node name associated with LoadBalancer IPs in your Kubernetes cluster.\x1b[0m\n") // Italics
}

func getLoadBalancerIPsStartingWithSeven(clientset *kubernetes.Clientset) []string {
	var lbIPs []string

	// Get LoadBalancer services
	services, err := clientset.CoreV1().Services("").List(context.TODO(), v1.ListOptions{})
	if err != nil {
		fmt.Printf("Error fetching services: %v\n", err)
		return lbIPs
	}

	// Collect LoadBalancer IPs
	for _, service := range services.Items {
		if service.Spec.Type == "LoadBalancer" {
			for _, ingress := range service.Status.LoadBalancer.Ingress {
				if strings.HasPrefix(ingress.IP, "7") {
					lbIPs = append(lbIPs, ingress.IP)
				}
			}
		}
	}

	return lbIPs
}

func getSpecificLoadBalancerIPs(reader *bufio.Reader) []string {
	fmt.Print("\nEnter LB IP(s) separated by comma: ")
	lbIPsStr, _ := reader.ReadString('\n')
	lbIPsStr = strings.TrimSpace(lbIPsStr)
	lbIPs := strings.Split(lbIPsStr, ",")
	return lbIPs
}

func getAllNodes(clientset *kubernetes.Clientset) ([]string, error) {
	var nodes []string

	// Get all nodes in the cluster
	nodeList, err := clientset.CoreV1().Nodes().List(context.TODO(), v1.ListOptions{})
	if err != nil {
		return nil, err
	}

	// Collect node names
	for _, node := range nodeList.Items {
		nodes = append(nodes, node.Name)
	}

	return nodes, nil
}

func createInventoryFile(nodes []string, ansibleUsername string) error {
	// Create or overwrite k8s.inventory file
	file, err := os.Create("k8s.inventory")
	if err != nil {
		return err
	}
	defer file.Close()

	// Write inventory header
	_, err = file.WriteString("[k8s]\n")
	if err != nil {
		return err
	}

	// Write nodes to inventory file
	for _, node := range nodes {
		_, err := file.WriteString(fmt.Sprintf("%s ansible_user=%s\n", node, ansibleUsername))
		if err != nil {
			return err
		}
	}

	return nil
}

func loadingAnimation() func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		fmt.Println("\n*******************************************")
		fmt.Println("*** Please wait... I am working on it ***")
		fmt.Println("*******************************************")
		for {
			select {
			case <-stop:
				done <- struct{}{}
				return
			default:
				for _, char := range chars {
					fmt.Printf("\rWorking %s", char)
					time.Sleep(100 * time.Millisecond)
				}
			}
		}
	}()

	return func() {
		close(stop)
		<-done
		fmt.Print("\r                                            \r") // Clear spinner line
	}
}

func runARPCommandOnAllNodes(nodes []string, arpInterface string, lbIPs []string, ansibleUsername string) {
	var hostingNodes [][]string

	for _, node := range nodes {
		for _, ip := range lbIPs {
			cmd := exec.Command("ansible", "-i", "k8s.inventory", node, "-u", ansibleUsername, "-m", "shell", "-a", fmt.Sprintf("arping -q -I %s %s -c 1", arpInterface, ip))
			out, err := cmd.CombinedOutput()
			if err != nil {
				// If the output contains "FAILED", add the node to the list of LoadBalancer IP hosting nodes
				if strings.Contains(string(out), "FAILED") {
					hostingNodes = append(hostingNodes, []string{node, ip})
				}
				continue
			}
		}
	}

	// Print table with color
	fmt.Println("\nHere is your result:")
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Node Name", "LoadBalancer IP"})
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgHiRedColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgHiRedColor},
	)
	table.SetColumnColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgHiYellowColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgHiYellowColor},
	)

	for _, row := range hostingNodes {
		table.Append(row)
	}

	table.Render()
}

func removeInventoryFile() error {
	// Check if the file exists
	if _, err := os.Stat("k8s.inventory"); err == nil {
		// Remove the file
		if err := os.Remove("k8s.inventory"); err != nil {
			return err
		}
	}
	return nil
}

