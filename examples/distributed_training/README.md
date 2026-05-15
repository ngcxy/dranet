# Distributed Training MFU Benchmark

This example runs a PyTorch DistributedDataParallel benchmark over NCCL and
reports model FLOPs utilization (MFU). It follows the DRA + MPIJob pattern used
by the Azure AKS examples, but targets 8 NVIDIA GPUs and 8 dranet RDMA NICs per
worker node.

## Files

| File | Description |
|---|---|
| `device-class.yaml` | `DeviceClass` selecting dranet-published RDMA devices (`driver == "dra.net"`) |
| `benchmark-configmap.yaml` | Shared PyTorch benchmark script used by all MPIJobs |
| `resource-claim-template.yaml` | `ResourceClaimTemplate` objects for 8 GPU/8 NIC and 4 GPU/4 NIC NUMA comparison runs |
| `mpi-job.yaml` | `MPIJob` and benchmark `ConfigMap` for the 8 GPU/8 NIC PyTorch/NCCL MFU run |
| `mpi-job-4gpu-4nic-numa-aligned.yaml` | 4 GPU/4 NIC MPIJob using GPUs 0-3 with NUMA-0 NICs (`mlx5_0`-`mlx5_3`) |
| `mpi-job-4gpu-4nic-numa-unaligned.yaml` | 4 GPU/4 NIC MPIJob using GPUs 0-3 with NUMA-1 NICs (`mlx5_4`-`mlx5_7`) |

## Run

Install the MPI Operator if the cluster does not already have the `MPIJob` CRD:

```bash
kubectl apply --server-side -k \
  "https://github.com/kubeflow/mpi-operator/manifests/overlays/standalone?ref=v0.7.0"
```

Apply the example and stream the launcher logs:

```bash
kubectl apply -f benchmark-configmap.yaml
kubectl apply -f device-class.yaml
kubectl apply -f resource-claim-template.yaml
kubectl apply -f mpi-job.yaml

kubectl wait --for=condition=ready \
  pod -l training.kubeflow.org/job-name=pytorch-nccl-mfu,training.kubeflow.org/job-role=worker \
  --timeout=900s

launcher=$(kubectl get pods \
  -l training.kubeflow.org/job-name=pytorch-nccl-mfu,training.kubeflow.org/job-role=launcher \
  -o jsonpath='{.items[0].metadata.name}')
kubectl logs -f "${launcher}"
```

The NGC PyTorch image includes the OpenSSH client used by `mpirun`, but some
tags do not include `sshd`; the worker startup installs `openssh-server` when it
is missing.

The benchmark prints a final line like:

```text
MFU_RESULT backend=nccl ... tflops_per_gpu=... global_tflops=... mfu_percent=...
```

## NUMA Comparison

On `Standard_ND96isr_H100_v5`, `nvidia-smi topo -m` shows GPUs 0-3 are NUMA-0
and GPUs 4-7 are NUMA-1. The 4 GPU comparison keeps GPUs fixed to GPUs 0-3 and
varies only the allocated NICs:

| Case | GPUs | NICs | GPU/NIC path |
|---|---|---|---|
| `h100-4gpu-4nic-numa-aligned` | PCI `0001`, `0002`, `0003`, `0008` | NUMA-0 `mlx5_0`-`mlx5_3` | `NODE` |
| `h100-4gpu-4nic-numa-unaligned` | PCI `0001`, `0002`, `0003`, `0008` | NUMA-1 `mlx5_4`-`mlx5_7` | `SYS` |

Run either comparison manifest after applying the shared benchmark `ConfigMap`:

```bash
kubectl apply -f benchmark-configmap.yaml
kubectl apply -f mpi-job-4gpu-4nic-numa-aligned.yaml
kubectl apply -f mpi-job-4gpu-4nic-numa-unaligned.yaml
```

### Results

Cluster: 2 × `Standard_ND96isr_H100_v5` workers, 4 H100 GPUs per worker, 4 RDMA
NICs per worker, BF16 DDP over NCCL.

| Run | Case | Avg step | TFLOP/s per GPU | Global TFLOP/s | MFU |
|---:|---|---:|---:|---:|---:|
| 1 | NUMA aligned | 55.496 ms | 356.62 | 2852.97 | 36.06% |
| 2 | NUMA aligned | 55.842 ms | 354.42 | 2835.33 | 35.84% |
| 3 | NUMA aligned | 55.615 ms | 355.86 | 2846.87 | 35.98% |
| 1 | NUMA unaligned | 60.951 ms | 324.70 | 2597.64 | 32.83% |
| 2 | NUMA unaligned | 60.884 ms | 325.06 | 2600.51 | 32.87% |
| 3 | NUMA unaligned | 60.865 ms | 325.17 | 2601.34 | 32.88% |

Best aligned result: run 1, `36.06%` MFU, `356.62` TFLOP/s per GPU,
`55.496 ms` average step.

Worst unaligned result: run 1, `32.83%` MFU, `324.70` TFLOP/s per GPU,
`60.951 ms` average step.

Largest observed gap between the best aligned run and worst unaligned run:
`3.23` MFU percentage points, or a `9.84%` relative MFU lift. By step time,
the aligned run was `8.95%` faster.

## Benchmark Shape

The default run uses 2 workers, 8 ranks per worker, and one BF16 DDP rank per
GPU. Each rank trains a stack of dense layers and measures only the timed
training steps after warmup.

MFU is computed as:

```text
MFU = achieved_training_TFLOP/s_per_GPU / theoretical_peak_TFLOP/s_per_GPU
```

The model FLOPs count includes the linear-layer forward matmul, input-gradient
matmul, and weight-gradient matmul:

```text
6 * batch_size_per_gpu * hidden_size * hidden_size * layers
```

Default environment variables in `mpi-job.yaml`:

| Variable | Default | Description |
|---|---:|---|
| `MFU_BATCH_SIZE` | `4096` | Local batch size per GPU |
| `MFU_HIDDEN_SIZE` | `8192` | Dense layer width |
| `MFU_LAYERS` | `12` | Number of dense layers |
| `MFU_WARMUP_STEPS` | `5` | Untimed warmup steps |
| `MFU_STEPS` | `20` | Timed steps |
| `GPU_PEAK_TFLOPS` | `989` | Dense BF16 peak TFLOP/s per NVIDIA H100 SXM GPU |

Update `GPU_PEAK_TFLOPS` when running on another GPU type.
