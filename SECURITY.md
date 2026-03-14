# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in go-smsc, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, email: **security@idnteq.com**

Include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

We will acknowledge receipt within 48 hours and aim to release a fix within 7 days for critical issues.

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | Yes       |

## Security Considerations

When deploying the SMSC Gateway in production:

- **TLS**: Enable TLS for both northbound (SMPP) and HTTP (REST API/Admin) connections
- **API Keys**: Use strong, unique API keys for REST API access
- **Admin Password**: Change the default admin password immediately after first run
- **JWT Secret**: Set `GW_JWT_SECRET` to a strong random value in production
- **Network**: Restrict access to SMPP (2776), HTTP (8080), and metrics (9090) ports
- **Pebble Data**: Protect the data directory — it contains message content and credentials
