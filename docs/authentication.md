# Authentication

This document describes the Central Authentication Service (CAS) single sign-on procedure.

## Authentication Overview

ETHOL uses the PENS CAS server for user authentication.
The `AuthManager` component controls the authentication lifecycle.
All HTTP requests share a cookie jar to store session cookies.

## The Login Flow

The initial login executes four sequential steps:

```
+---------------+              +---------------+              +---------------+
|  AuthManager  |              |   ETHOL API   |              |    CAS SSO    |
+-------+-------+              +-------+-------+              +-------+-------+
        |                              |                              |
        | 1. GET /api/auth/cas-redirect|                              |
        |----------------------------->|                              |
        | 2. Redirect to CAS form      |                              |
        |<-----------------------------+----------------------------->|
        |                              |                              |
        | 3. POST username & password  |                              |
        |------------------------------------------------------------>|
        | 4. Return session cookies    |                              |
        |<------------------------------------------------------------+
        |                              |                              |
        | 5. GET /api/auth/validasi-token                             |
        |----------------------------->|                              |
        | 6. Return UserInfo JSON      |                              |
        |<-----------------------------|                              |
```

### Step 1: Request Redirect URL

The client sends an HTTP `GET` request to:
```
https://ethol.pens.ac.id/api/auth/cas-redirect
```
The ETHOL server redirects the client to the CAS login page on `online.mis.pens.ac.id`.

### Step 2: Parse CAS HTML Form

The client receives the CAS login HTML page.
An HTML tokenizer reads the stream to locate `<form id="fm1">`.
The parser extracts:
- The form action URL.
- The hidden `execution` input value.
- The hidden `_eventId` input value.
- The hidden `geolocation` input value.

### Step 3: Send Credentials

The client sends an HTTP `POST` request to the form action URL.
The request contains `application/x-www-form-urlencoded` data:
- `username`: The student NRP or account ID.
- `password`: The account password.
- `execution`: The token from the form.
- `_eventId`: The event value (usually `submit`).
- `geolocation`: An empty string or coordinate value.

The CAS server responds with a redirect back to ETHOL.
The shared cookie jar stores all session cookies from these responses.

### Step 4: Validate Token and Identity

The client sends an HTTP `GET` request to:
```
https://ethol.pens.ac.id/api/auth/validasi-token
```
The server responds with a JSON object that contains user information:
```json
{
  "nomor": 12345,
  "nama": "STUDENT NAME",
  "nipnrp": "3120500001"
}
```
The client decodes this object into a `UserInfo` struct.
This completes the login process.

## Session Maintenance

The application keeps sessions active without a full login when possible.

### Refresh Session

The `AuthManager.EnsureSession()` method runs before every scan operation.
If a user session already exists:
1. The client sends a `POST` request to `/api/auth/refresh`.
2. If the response status is `200 OK`, the session remains valid.
3. If the response status is not `200 OK`, the client runs the full login process again.

### Re-login Note

In `commands.go`, the code handles user `/relogin` commands from Telegram.
When a complete session reset is necessary, the system clears current user data and repeats the full four-step login sequence.
