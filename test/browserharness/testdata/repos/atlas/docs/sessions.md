# Session contract

Atlas uses strict server-side expiry:

- A token is valid only while `now` is strictly before `ExpiresAt`.
- At the exact expiry instant, and after it, the token is expired.
- There is no grace period. Clients may refresh early, but the server never
  accepts an expired token, including on the refresh path.
