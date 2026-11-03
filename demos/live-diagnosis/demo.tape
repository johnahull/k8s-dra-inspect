# dra-inspect live diagnosis recording
# Run against the live MI300X cluster with the mi300x-inference-numa offering.

Output demo.gif
Output demo.mp4

Set Shell "bash"
Set FontSize 14
Set Width 1400
Set Height 1200
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms

Hide
Type "export PATH=$HOME/.local/bin:$PATH"
Enter
Sleep 5s
Type "export DEMO=$HOME/devel/k8s-dra-inspect/demos/live-diagnosis"
Enter
Sleep 5s
Type "kubectl delete pod demo-mi300x-inference-pod -n default --ignore-not-found --wait=true --timeout=180s >/dev/null 2>&1"
Enter
Sleep 3s
Type "kubectl delete resourceclaim demo-mi300x-inference -n default --ignore-not-found --wait=true --timeout=60s >/dev/null 2>&1"
Enter
Sleep 3s
Type "kubectl apply -n default -f $DEMO/pod-example.yaml >/dev/null 2>&1"
Enter
Sleep 5s
Type "kubectl wait --for=condition=Ready pod/demo-mi300x-inference-pod -n default --timeout=180s >/dev/null 2>&1"
Enter
Sleep 5s
Type "clear"
Enter
Sleep 5s
Show

# 1. Show the published DRA drivers and hardware inventory.
Type "dra-inspect inspect drivers"
Enter
Sleep 10s
Type "clear"
Enter
Sleep 5s
Type "dra-inspect inspect slices"
Enter
Sleep 12s
Type "clear"
Enter
Sleep 5s

# 2. Show the published topology and readable device attributes.
Type "dra-inspect inspect topology --compact"
Enter
Sleep 12s
Type "clear"
Enter
Sleep 5s
Type "dra-inspect inspect attributes --topology | more"
Enter
Sleep 15s
Type " "
Sleep 10s
Type " "
Sleep 10s
Type " "
Sleep 10s
Type " "
Sleep 10s
Type "clear"
Enter
Sleep 5s

# 3. Show the allocated claim and validate its placement and capacity.
Type "dra-inspect inspect claims -n default"
Enter
Sleep 12s
Type "clear"
Enter
Sleep 5s
Type "dra-inspect check alignment demo-mi300x-inference -n default"
Enter
Sleep 12s
Type "clear"
Enter
Sleep 5s
Type "dra-inspect check counters"
Enter
Sleep 15s
Type "clear"
Enter
Sleep 5s
Type "dra-inspect check capacity"
Enter
Sleep 15s
Type "clear"
Enter
Sleep 5s
Type "dra-inspect check topology"
Enter
Sleep 15s
Type "clear"
Enter
Sleep 5s

# Cleanup only the resources created by this recording.
Hide
Type "kubectl delete pod demo-mi300x-inference-pod -n default --ignore-not-found --wait=true --timeout=180s >/dev/null 2>&1"
Enter
Sleep 3s
Type "kubectl delete resourceclaim demo-mi300x-inference -n default --ignore-not-found --wait=true --timeout=60s >/dev/null 2>&1"
Enter
Sleep 3s
Show
