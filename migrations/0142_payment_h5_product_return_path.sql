-- Owner: Payment. Forward-only; preserve every existing OAuth state.
-- Standard products may now authorize and return to their same /p/{code}
-- checkout. Keep one segment and reject encoded separators/open redirects.
ALTER TABLE payment_h5_oauth_states
  DROP CONSTRAINT payment_h5_oauth_states_return_path_check;
ALTER TABLE payment_h5_oauth_states
  ADD CONSTRAINT payment_h5_oauth_states_return_path_check CHECK (
    return_path ~ '^/(p/[^/?#]+|pay/[^/?#]+|s/[^/?#]+(/pay)?|c/[a-z][a-z0-9-]{5,119})$'
    AND return_path !~ '%(2[fF]|5[cC]|3[fF]|23)'
    AND position(E'\\' in return_path)=0
  );
