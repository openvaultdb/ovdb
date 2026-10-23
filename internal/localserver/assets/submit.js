// Submits the sign-in form as soon as the page loads (REQ:login-exchange-on-post).
// A file, not an inline script, because the Content-Security-Policy is
// default-src 'self'.
document.getElementById('login').submit()
