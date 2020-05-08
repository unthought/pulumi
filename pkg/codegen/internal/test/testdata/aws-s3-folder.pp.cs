using Pulumi;
using Aws = Pulumi.Aws;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text.Json;

class MyStack : Stack
{
    public MyStack()
    {
        // Create a bucket and expose a website index document
        var siteBucket = new Aws.S3.Bucket("siteBucket", new Aws.S3.BucketArgs
        {
            Website = new Aws.S3.BucketArgs
            {
                IndexDocument = "index.html",
            },
        });
        var siteDir = "www";
        // For each file in the directory, create an S3 object stored in `siteBucket`
        var files = new List<Aws.S3.BucketObject>();
        foreach (var range in Directory.GetFiles(siteDir).Select(Path.GetFileName).Select((v, k) => new { Key = k, Value = v }))
        {
            files.Add(new Aws.S3.BucketObject($"files-{range.Key}", new Aws.S3.BucketObjectArgs
            {
                Bucket = siteBucket.Id,
                Key = range.Value,
                Source = new FileAsset($"{siteDir}/{range.Value}"),
                ContentType = /* TODO ("FunctionCallExpression: mimeType (aws-s3-folder.pp:19,16-37)")*/,
            }));
        }
        // set the MIME type of the file
        // Set the access policy for the bucket so all objects are readable
        var bucketPolicy = new Aws.S3.BucketPolicy("bucketPolicy", new Aws.S3.BucketPolicyArgs
        {
            Bucket = siteBucket.Id,
            Policy = siteBucket.Id.Apply(id => JsonSerializer.Serialize(new Aws.S3.BucketPolicyArgs
            {
                Version = "2012-10-17",
                Statement = 
                {
                    new Aws.S3.BucketPolicyArgs
                    {
                        Effect = "Allow",
                        Principal = "*",
                        Action = 
                        {
                            "s3:GetObject",
                        },
                        Resource = 
                        {
                            $"arn:aws:s3:::{id}/*",
                        },
                    },
                },
            })),
        });
        this.BucketName = siteBucket.Bucket;
        this.WebsiteUrl = siteBucket.WebsiteEndpoint;
    }

    [Output("bucketName")] public Output<string> BucketName { get; set; }
    [Output("websiteUrl")] public Output<string> WebsiteUrl { get; set; }
}
