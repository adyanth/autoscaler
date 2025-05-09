# Proxmox Cloud Provider for Cluster Autoscaling

## How to create a template LXC container
[This gist](https://gist.github.com/triangletodd/02f595cd4c0dc9aac5f7763ca2264185) has good information on creating such a template.
Along with it, ensure the following are satisfied:

* Set a container ID that has free IDs following it. Each new worker will have a sequential ID following this container ID
* `curl` is installed in the container (necessary for `k3s` installation)
* The `automation_key` supplied below is added to the `authorized_keys` (either during container creation or manually later)
* If using Longhorn, install `open-iscsi` and `nfs-common` packages as well


## Autoscaler Config

```json
{
    "proxmoxConfig": {
        "apiEndpoint": "https://proxmox.cluster.local:8006/api2/json",
        "apiUser": "root@pam!autoscaling",
        "apiToken": "sample-api-token",
        "insecureSkipVerify": true,
        "timeoutSeconds": 30
    },
    "nodeConfigs": [
        {
            "refCtrId": 600,
            "targetPool": "Autoscaling",
            "workerNamePrefix": "k8s-worker-autoscaled",
            "minSize": 0,
            "maxSize": 10
        }
    ],
    "k3sConfig": {
        "sshKeyFile": "automation_key",
        "serverUser": "master",
        "serverHost": "k8s-master.cluster.local",
        "user": "root"
    }
}
```

## Permissions

The API token needs the following permissions with propagate enabled:

|       **Path**       |      **Role**      |            **Reason**           |
|:---------------------|:-------------------|:--------------------------------|
| `/`                  | `PVEAuditor`       | Reading node and template info  |
| `/`                  | `PVETemplateUser`  | Using the template              |
| `/pool/{targetPool}` | `PVEPoolUser`      | List the relevant pools         |
| `/pool/{targetPool}` | `PVEVMAdmin`       | Create/Delete containers        |
| `/storage/local-lvm` | `PVEDatastoreUser` | Allocate storage for containers |
| `/sdn/zones`         | `PVESDNUser`       | Attach to network               |
