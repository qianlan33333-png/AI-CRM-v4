// The build applies the reviewed presentation-only derived source. Validation,
// hash-token removal and the public, credential-free read protocol stay owned
// by the existing member-grid module.
import { mountMemberGridShare } from '../src/public/memberGridShare';

const stage = document.getElementById('stage');
if (stage) void mountMemberGridShare(stage);
