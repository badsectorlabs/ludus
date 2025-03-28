---
title: Lost API Key
---

### Recover an API key for a user if an admin key is known

1. Run `ludus user apikey --user <userID>` 

### Recover an API key using the `ROOT` key (no admin key is known)

1. SSH into the Ludus host as root and run `ludus-install-status` which will print the `ROOT` key
2. Use the `ROOT` key with the client to reset the api key of the user with the lost key

```
LUDUS_API_KEY='ROOT.o>T3BMm!^\As_0Fhve8B\VrD&zqc#kCk&B&?e|aF' ludus user apikey --user <userID>
```