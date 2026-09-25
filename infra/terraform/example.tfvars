# Copy to terraform.tfvars (gitignored) and adjust. Then:
#   terraform init && terraform plan -var-file=terraform.tfvars
region             = "us-east-1"
cluster_name       = "deploy-platform"
kubernetes_version = "1.30"
node_instance_type = "t3.medium"
node_desired_size  = 1
node_max_size      = 3
platform_domain    = "apps.example.com"
