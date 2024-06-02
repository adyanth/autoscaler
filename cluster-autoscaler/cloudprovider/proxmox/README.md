
## Autoscaler Config

```json
{
    "insecureSkipVerify": true,
    "apiEndpoint": "https://proxmox.cluster.local:8006/api2/json",
    "apiUser": "root@pam!autoscaling",
    "apiToken": "sample-api-token",
    "nodeConfigs": [
        {
            "refCtrId": 600,
            "targetPool": "Autoscaling",
            "workerNamePrefix": "k8s-worker-autoscaled",
            "minSize": 0,
            "maxSize": 10,
        }
    ],
    "timeoutSeconds": 30,
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

|      **Path**      |     **Role**     |            **Reason**           |
|:------------------:|:----------------:|:-------------------------------:|
| /                  | PVEAuditor       | Reading node and template info  |
| /                  | PVETemplateUser  | Using the template              |
| /pool/{poolName}   | PVEVMAdmin       | Create/Delete containers        |
| /storage/local-lvm | PVEDatastoreUser | Allocate storage for containers |
| /sdn/zones         | PVESDNUser       | Attach to network               |
