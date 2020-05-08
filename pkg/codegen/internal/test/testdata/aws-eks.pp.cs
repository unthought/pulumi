using Pulumi;
using Aws = Pulumi.Aws;
using System.Collections.Generic;
using System.Text.Json;

class MyStack : Stack
{
    public MyStack()
    {
        // VPC
        var eksVpc = new Aws.Ec2.Vpc("eksVpc", new Aws.Ec2.VpcArgs
        {
            CidrBlock = "10.100.0.0/16",
            InstanceTenancy = "default",
            EnableDnsHostnames = true,
            EnableDnsSupport = true,
            Tags = new Aws.Ec2.VpcArgs
            {
                { "Name", "pulumi-eks-vpc" },
            },
        });
        var eksIgw = new Aws.Ec2.InternetGateway("eksIgw", new Aws.Ec2.InternetGatewayArgs
        {
            VpcId = eksVpc.Id,
            Tags = new Aws.Ec2.InternetGatewayArgs
            {
                { "Name", "pulumi-vpc-ig" },
            },
        });
        var eksRouteTable = new Aws.Ec2.RouteTable("eksRouteTable", new Aws.Ec2.RouteTableArgs
        {
            VpcId = eksVpc.Id,
            Routes = 
            {
                new Aws.Ec2.RouteTableArgs
                {
                    CidrBlock = "0.0.0.0/0",
                    GatewayId = eksIgw.Id,
                },
            },
            Tags = new Aws.Ec2.RouteTableArgs
            {
                { "Name", "pulumi-vpc-rt" },
            },
        });
        // Subnets, one for each AZ in a region
        var zones = Output.Create(Aws.GetAvailabilityZones.InvokeAsync(new Aws.GetAvailabilityZonesArgs{}));
        var vpcSubnet = new List<Aws.Ec2.Subnet>();
        foreach (var range in (await zones.Names).Select((v, k) => new { Key = k, Value = v }))
        {
            vpcSubnet.Add(new Aws.Ec2.Subnet($"vpcSubnet-{range.Key}", new Aws.Ec2.SubnetArgs
            {
                AssignIpv6AddressOnCreation = false,
                VpcId = eksVpc.Id,
                MapPublicIpOnLaunch = true,
                CidrBlock = $"10.100.{range.Key}.0/24",
                AvailabilityZone = range.Value,
                Tags = new Aws.Ec2.SubnetArgs
                {
                    { "Name", $"pulumi-sn-{range.Value}" },
                },
            }));
        }
        var rta = new List<Aws.Ec2.RouteTableAssociation>();
        foreach (var range in (await zones.Names).Select((v, k) => new { Key = k, Value = v }))
        {
            rta.Add(new Aws.Ec2.RouteTableAssociation($"rta-{range.Key}", new Aws.Ec2.RouteTableAssociationArgs
            {
                RouteTableId = eksRouteTable.Id,
                SubnetId = vpcSubnet.Apply(vpcSubnet => vpcSubnet[range.Key].Id),
            }));
        }
        var subnetIds = vpcSubnet.Apply(vpcSubnet => vpcSubnet.Select(v => v.__item.Id));
        var eksSecurityGroup = new Aws.Ec2.SecurityGroup("eksSecurityGroup", new Aws.Ec2.SecurityGroupArgs
        {
            VpcId = eksVpc.Id,
            Description = "Allow all HTTP(s) traffic to EKS Cluster",
            Tags = new Aws.Ec2.SecurityGroupArgs
            {
                { "Name", "pulumi-cluster-sg" },
            },
            Ingress = 
            {
                new Aws.Ec2.SecurityGroupArgs
                {
                    CidrBlocks = 
                    {
                        "0.0.0.0/0",
                    },
                    FromPort = 443,
                    ToPort = 443,
                    Protocol = "tcp",
                    Description = "Allow pods to communicate with the cluster API Server.",
                },
                new Aws.Ec2.SecurityGroupArgs
                {
                    CidrBlocks = 
                    {
                        "0.0.0.0/0",
                    },
                    FromPort = 80,
                    ToPort = 80,
                    Protocol = "tcp",
                    Description = "Allow internet access to pods",
                },
            },
        });
        // EKS Cluster Role
        var eksRole = new Aws.Iam.Role("eksRole", new Aws.Iam.RoleArgs
        {
            AssumeRolePolicy = JsonSerializer.Serialize(new Aws.Iam.RoleArgs
            {
                { "Version", "2012-10-17" },
                { "Statement", 
                {
                    new Aws.Iam.RoleArgs
                    {
                        { "Action", "sts:AssumeRole" },
                        { "Principal", new Aws.Iam.RoleArgs
                        {
                            { "Service", "eks.amazonaws.com" },
                        } },
                        { "Effect", "Allow" },
                        { "Sid", "" },
                    },
                } },
            }),
        });
        var servicePolicyAttachment = new Aws.Iam.RolePolicyAttachment("servicePolicyAttachment", new Aws.Iam.RolePolicyAttachmentArgs
        {
            Role = eksRole.Id,
            PolicyArn = "arn:aws:iam::aws:policy/AmazonEKSServicePolicy",
        });
        var clusterPolicyAttachment = new Aws.Iam.RolePolicyAttachment("clusterPolicyAttachment", new Aws.Iam.RolePolicyAttachmentArgs
        {
            Role = eksRole.Id,
            PolicyArn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy",
        });
        // EC2 NodeGroup Role
        var ec2Role = new Aws.Iam.Role("ec2Role", new Aws.Iam.RoleArgs
        {
            AssumeRolePolicy = JsonSerializer.Serialize(new Aws.Iam.RoleArgs
            {
                { "Version", "2012-10-17" },
                { "Statement", 
                {
                    new Aws.Iam.RoleArgs
                    {
                        { "Action", "sts:AssumeRole" },
                        { "Principal", new Aws.Iam.RoleArgs
                        {
                            { "Service", "ec2.amazonaws.com" },
                        } },
                        { "Effect", "Allow" },
                        { "Sid", "" },
                    },
                } },
            }),
        });
        var workerNodePolicyAttachment = new Aws.Iam.RolePolicyAttachment("workerNodePolicyAttachment", new Aws.Iam.RolePolicyAttachmentArgs
        {
            Role = ec2Role.Id,
            PolicyArn = "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
        });
        var cniPolicyAttachment = new Aws.Iam.RolePolicyAttachment("cniPolicyAttachment", new Aws.Iam.RolePolicyAttachmentArgs
        {
            Role = ec2Role.Id,
            PolicyArn = "arn:aws:iam::aws:policy/AmazonEKSCNIPolicy",
        });
        var registryPolicyAttachment = new Aws.Iam.RolePolicyAttachment("registryPolicyAttachment", new Aws.Iam.RolePolicyAttachmentArgs
        {
            Role = ec2Role.Id,
            PolicyArn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
        });
        // EKS Cluster
        var eksCluster = new Aws.Eks.Cluster("eksCluster", new Aws.Eks.ClusterArgs
        {
            RoleArn = eksRole.Arn,
            Tags = new Aws.Eks.ClusterArgs
            {
                { "Name", "pulumi-eks-cluster" },
            },
            VpcConfig = new Aws.Eks.ClusterArgs
            {
                PublicAccessCidrs = 
                {
                    "0.0.0.0/0",
                },
                SecurityGroupIds = 
                {
                    eksSecurityGroup.Id,
                },
                SubnetIds = subnetIds,
            },
        });
        var nodeGroup = new Aws.Eks.NodeGroup("nodeGroup", new Aws.Eks.NodeGroupArgs
        {
            ClusterName = eksCluster.Name,
            NodeGroupName = "pulumi-eks-nodegroup",
            NodeRoleArn = ec2Role.Arn,
            SubnetIds = subnetIds,
            Tags = new Aws.Eks.NodeGroupArgs
            {
                { "Name", "pulumi-cluster-nodeGroup" },
            },
            ScalingConfig = new Aws.Eks.NodeGroupArgs
            {
                DesiredSize = 2,
                MaxSize = 2,
                MinSize = 1,
            },
        });
        this.ClusterName = eksCluster.Name;
        this.Kubeconfig = Output.Tuple(eksCluster.Endpoint, eksCluster.CertificateAuthority, eksCluster.Name).Apply(values =>
        {
            var endpoint = values.Item1;
            var certificateAuthority = values.Item2;
            var name = values.Item3;
            return JsonSerializer.Serialize(new Args
            {
                ApiVersion = "v1",
                Clusters = 
                {
                    new Args
                    {
                        Cluster = new Args
                        {
                            Server = endpoint,
                            { "certificate-authority-data", certificateAuthority.Data },
                        },
                        Name = "kubernetes",
                    },
                },
                Contexts = 
                {
                    new Args
                    {
                        Contest = new Args
                        {
                            Cluster = "kubernetes",
                            User = "aws",
                        },
                    },
                },
                { "current-context", "aws" },
                Kind = "Config",
                Users = 
                {
                    new Args
                    {
                        Name = "aws",
                        User = new Args
                        {
                            Exec = new Args
                            {
                                ApiVersion = "client.authentication.k8s.io/v1alpha1",
                                Command = "aws-iam-authenticator",
                            },
                            Args = 
                            {
                                "token",
                                "-i",
                                name,
                            },
                        },
                    },
                },
            });
        });
    }

    [Output("clusterName")] public Output<string> ClusterName { get; set; }
    [Output("kubeconfig")] public Output<string> Kubeconfig { get; set; }
}
