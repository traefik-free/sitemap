# Traefik SEO Plugin
![Traefik SEO Plugin](.assets/icon.png)
This plugin is a middleware for [Traefik](https://traefik.io/), designed to dynamically generate sitemap.xml and robots.txt files based on the paths served by your application. It automatically collects successful (HTTP 200) routes that are not ignored and includes them in the sitemap. This helps improve SEO by providing search engines with an up-to-date site map and robots instructions.

## Features
- Dynamic Sitemap Generation: Collects URLs from successful requests (HTTP 200) and generates an XML sitemap on-the-fly when requested.
- Robots.txt Support: Generates a simple robots.txt file that references the sitemap URL and allows all user agents.
- Configurable Paths: Customize the paths for sitemap (/sitemap.xml by default) and robots (/robots.txt by default).
- Ignore Patterns: Exclude specific paths using regular expressions. Includes default ignores for common non-SEO files (e.g., images, scripts, backups, etc.).
- Host-Aware Filtering: Only includes URLs matching the current host in the sitemap.
- Priorities and Lastmod: Assigns priorities (1.0 for root, 0.8 for others) and uses the current UTC time for lastmod.
- Thread-Safe: Uses mutex locking for concurrent access to the path map.
- Gzip Handling: Properly handles gzipped responses when injecting Google Tag Manager scripts, ensuring content integrity.
- Google Tag Manager Integration: Optionally injects GTM script and noscript tags into HTML responses for analytics tracking.

## Installation
This plugin is written in Go and can be integrated as a Traefik middleware plugin. To use it:

1. Build the Plugin: Clone the repository and build the plugin binary if needed, or use it directly in your Traefik configuration.
  
2. Traefik Configuration: Enable experimental plugins in your Traefik static configuration (e.g., traefik.toml or YAML):
   ```yaml
   experimental:
     plugins:
       seo:
         moduleName: "github.com/traefik-free/seo" # Replace with the actual Go module path
         version: "v0.1.2" # Specify the version
   ```
  
   For more details on Traefik plugins, refer to the [Traefik Plugin Documentation](https://doc.traefik.io/traefik/plugins/overview/).

## Configuration
The plugin accepts a JSON configuration with the following options:
- sitemapPath (string, optional): Path where the sitemap is served. Default: /sitemap.xml.
- robotsPath (string, optional): Path where robots.txt is served. Default: /robots.txt.
- ignore (array of strings, optional): List of regex patterns to ignore when collecting paths for the sitemap. These are compiled as Go regular expressions.
- gtmID (string, optional): Google Tag Manager container ID (e.g., "GTM-XXXXXX"). If provided, the plugin will automatically inject the GTM script into the <head> and noscript iframe into the <body> of HTML responses. This enables easy analytics tracking without modifying your application code.

### Default Ignore Patterns
The plugin includes built-in ignore patterns to exclude non-SEO-relevant files and paths:
- Case-insensitive: .env, .bak, .old, .example, .exmaple, .sample, .tmpl, .tpl, .dist, .~, .php, .aspx, config, wp-, sitemap, undefined.
- Paths starting with /_next/*.
- File extensions: .jpg, .jpeg, .png, .gif, .webp, .svg, .bmp, .tif, .tiff, .ico, .txt, .php, .exe, .css, .js, .json, .pdf, .doc, .docx, .xls, .xlsx, .ppt, .pptx, .mp3, .mp4, .avi, .mov, .zip, .rar, .tar, .gz, .env, .html, .xml.

You can add custom ignores via the configuration.

### Example Traefik Configuration (YAML)
Attach the middleware to a router in your dynamic configuration:
```yaml
http:
  middlewares:
    seo-middleware:
      plugin:
        seo:
          sitemapPath: "/sitemap.xml"
          robotsPath: "/robots.txt"
          gtmID: "GTM-0000000"  # Your Google Tag Manager ID
          ignore:
            - "^/admin/.*" # Custom ignore for admin paths
            - ".*\\.log$" # Ignore log files
  routers:
    my-router:
      rule: "Host(`example.com`)"
      service: "my-service"
      middlewares:
        - seo-middleware
```

## Usage
1. Attach to Routers: Add the middleware to your Traefik routers. As requests are handled successfully (200 OK), non-ignored paths are collected.
  
2. Access Sitemap: Visit https://yourdomain.com/sitemap.xml (or your configured path). The sitemap will include all collected URLs, sorted alphabetically, with priorities and lastmod timestamps.
3. Access Robots.txt: Visit https://yourdomain.com/robots.txt. It will contain:
   ```
   User-agent: *
   Sitemap: https://yourdomain.com/sitemap.xml
   ```

4. Path Collection: Only paths that return HTTP 200 and do not match ignore patterns are added. The root path (/) is always included if not present.
5. GTM Integration: If gtmID is set, HTML responses (text/html, status 200) will have the GTM script added before </head> and noscript after <body>. Gzipped responses are decompressed, modified, and re-gzipped automatically.

## How It Works
- Request Handling: The middleware wraps the next handler. For non-special paths, it records successful requests.
- Sitemap Build: When /sitemap.xml is requested, it locks the path map, filters by host, sorts, and generates XML.
- Robots Build: When /robots.txt is requested, it generates a simple text file with the sitemap reference.
- Scheme Detection: Uses X-Forwarded-Proto or falls back to the request scheme for full URLs.
- GTM Injection: If gtmID is configured, injects GTM scripts into HTML responses, handling gzipped content by decompressing, modifying, and re-compressing if necessary.

## Limitations
- Dynamic Only: Paths are collected at runtime; no static scanning of routes.
- Memory Usage: Stores all unique paths in memory. Suitable for small to medium sites; for large sites, consider periodic flushing or external storage.
- No Change Frequency: Currently, no <changefreq> in sitemap (can be added if needed).
- Lastmod: Always set to the current time on generation, not per-path modification time.

## Contributing
Contributions are welcome! Feel free to open issues or pull requests for improvements, such as adding more features or optimizing performance.

## License
This plugin is licensed under the MIT License. See [LICENSE](LICENSE) for details.

Plugin link: https://plugins.traefik.io/plugins/6950e4534cda2b265225fa58/seo-generator