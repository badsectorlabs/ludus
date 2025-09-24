import argparse
import base64
import json
import time
import hmac
import hashlib

def generate_jwt(secret, role):
    """
    Generates a JWT with a specified role and secret.
    The token will expire 20 years from the time of creation.

    Args:
        secret: The secret key for signing the token.
        role: The role to be included in the token's payload.

    Returns:
        A valid JWT string.
    """
    header = {
        "alg": "HS256",
        "typ": "JWT"
    }

    # Set the 'issued at' time to the current Unix timestamp
    iat = int(time.time())

    # Calculate the 'expiration' time as 20 years from 'iat'
    # Seconds in 20 years = 20 years * 365.25 days/year * 24 hours/day * 3600 seconds/hour
    seconds_in_20_years = int(20 * 365.25 * 24 * 60 * 60)
    exp = iat + seconds_in_20_years

    payload = {
        "role": role,
        "iss": "supabase",
        "iat": iat,
        "exp": exp
    }

    # Encode the header and payload
    encoded_header = base64.urlsafe_b64encode(json.dumps(header).encode()).rstrip(b"=")
    encoded_payload = base64.urlsafe_b64encode(json.dumps(payload).encode()).rstrip(b"=")

    # Create the signature
    signature_input = encoded_header + b"." + encoded_payload
    signature = hmac.new(secret.encode(), signature_input, hashlib.sha256).digest()
    encoded_signature = base64.urlsafe_b64encode(signature).rstrip(b"=")

    # Concatenate to form the JWT
    jwt = encoded_header + b"." + encoded_payload + b"." + encoded_signature
    return jwt.decode()

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Generate a JWT token with a given secret and role, expiring in 20 years.")
    
    parser.add_argument("secret", help="The secret key for signing the token.")
    parser.add_argument("role", help="The role to include in the token's payload.")
    
    args = parser.parse_args()
    
    jwt_token = generate_jwt(args.secret, args.role)
    
    print(jwt_token)