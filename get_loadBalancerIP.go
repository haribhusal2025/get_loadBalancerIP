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

// ANSI color codes for terminal output
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorPurple = "\033[35m"
	ColorCyan   = "\033[36m"
	ColorWhite  = "\033[37m"
	Bold        = "\033[1m"
)

// Spinner animation characters
var (
	chars = []string{"|", "/", "-", "\\"}
)

func main() {
	// Get current user
	currentUser, err := user.Current()
	if err != nil {
		fmt.Printf("%sError getting current user: %v%s\n", ColorRed, err, ColorReset)
		os.Exit(1)
	}

	// Path to the kubeconfig file
	var kubeconfig string
	flag.StringVar(&kubeconfig, "kubeconfig", filepath.Join(currentUser.HomeDir, ".kube", "config"), "path to the kubeconfig file")
	flag.Parse()

	// Load kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Printf("%sError loading kubeconfig: %v%s\n", ColorRed, err, ColorReset)
		os.Exit(1)
	}

	// Create Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("%sError creating Kubernetes client: %v%s\n", ColorRed, err, ColorReset)
		os.Exit(1)
	}

	// Print welcome message
	printWelcomeMessage(currentUser)

	// Prompt user for Ansible username
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(ColorBlue, "\nEnter the Ansible username to run ARP command (Ex: johndoe or johndoe-adm): ", ColorReset)
	ansibleUsername, _ := reader.ReadString('\n')
	ansibleUsername = strings.TrimSpace(ansibleUsername)

	// Get all nodes in the cluster
	nodes, err := getAllNodes(clientset)
	if err != nil {
		fmt.Printf("%sError fetching nodes: %v%s\n", ColorRed, err, ColorReset)
		os.Exit(1)
	}

	// Create inventory file
	err = createInventoryFile(nodes, ansibleUsername)
	if err != nil {
		fmt.Printf("%sError creating inventory file: %v%s\n", ColorRed, err, ColorReset)
		os.Exit(1)
	}

	// Get interface name starting with '7' using Ansible
	arpInterface := getInterfaceNameStartingWithSeven()
	if arpInterface == "" {
		fmt.Println(ColorRed, "Failed to retrieve network interface starting with '7'. Please check your setup.", ColorReset)
		os.Exit(1)
	}

	// Prompt user for LB IPs
	fmt.Print(ColorBlue, "\nDo you want to get all LoadBalancer IPs ? (yes/no): ", ColorReset)
	option, _ := reader.ReadString('\n')
	option = strings.TrimSpace(option)

	// Get LB IPs based on user's choice
	var lbIPs []string
	if option == "yes" {
		lbIPs = getLoadBalancerIPsStartingWithSeven(clientset)
	} else if option == "no" {
		lbIPs = getSpecificLoadBalancerIPs(reader)
	} else {
		fmt.Println(ColorRed, "Invalid option. Please choose 'yes' or 'no'.", ColorReset)
		os.Exit(1)
	}

	// Run ARP command on all nodes
	stopSpinner := loadingAnimation()
	defer stopSpinner() // Ensure spinner stops at the end
	runARPCommandOnAllNodes(nodes, arpInterface, lbIPs, ansibleUsername)

	// Print the interface used for ARP command
	fmt.Printf("\nInterface Used to run ARP command: %s%s%s\n\n\n", ColorGreen, arpInterface, ColorReset)
	fmt.Printf("%s****%s\n\n", ColorPurple, ColorReset)

	// Remove the inventory file after displaying the final output
	err = removeInventoryFile()
	if err != nil {
		fmt.Printf("%sError removing inventory file: %v%s\n", ColorRed, err, ColorReset)
	}
}

func printWelcomeMessage(currentUser *user.User) {
	fmt.Println("\n*******************************************")
	fmt.Printf("%s*** Welcome, %s! ***%s\n", ColorGreen, currentUser.Username, ColorReset)
	fmt.Println("*******************************************")
	fmt.Printf("%sThis tool helps you find the node name associated with LoadBalancer IPs in your Kubernetes cluster.%s\n", ColorCyan, ColorReset) // Italics
}

func getLoadBalancerIPsStartingWithSeven(clientset *kubernetes.Clientset) []string {
	var lbIPs []string

	// Get LoadBalancer services
	services, err := clientset.CoreV1().Services("").List(context.TODO(), v1.ListOptions{})
	if err != nil {
		fmt.Printf("%sError fetching services: %v%s\n", ColorRed, err, ColorReset)
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
					fmt.Printf("\r%sWorking %s%s", ColorPurple, char, ColorReset)
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
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgRedColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgRedColor},
	)
	table.SetColumnColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgYellowColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgYellowColor},
	)

	for _, row := range hostingNodes {
		table.Append(row)
	}

	table.Render() // Render the table with color settings
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

func getInterfaceNameStartingWithSeven() string {
	// Run a command using Ansible to get the interface name whose IP starts with '7'
	cmd := exec.Command("ansible", "-i", "k8s.inventory", "k8s[1]", "-m", "shell", "-a", "ip route | awk '/7/ {print $3}' | head -2")
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("%sError executing Ansible command: %v%s\n", ColorRed, err, ColorReset)
		return "" // Return empty string or handle error appropriately
	}
	//fmt.Println("Command output:", string(out))
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) >= 3 {
		interfaceName := strings.TrimSpace(lines[2]) // Third line
		return interfaceName
	}
	return ""
}
