# zerolog context provenance

The analyzer asks whether every reachable zerolog output carries a context.
These terms describe the proof it requires.

## Language

**Context proof**:
Evidence that an output carries a context on every reachable path. A context
variable being available does not by itself constitute a context proof.

**Sink**:
A statically identifiable zerolog operation that writes output. A recognized
sink without a context proof requires a diagnostic unless explicitly suppressed.

**Escape**:
A tracked value becoming reachable by code or storage the analyzer cannot follow.
An escape permits later unseen changes, even after a new context proof is established.

**Function summary**:
The proven results, parameter effects, returned parameter identities and possible
parameter escapes of a function, used when analyzing its callers.

**Parameter effect**:
A function's proven postcondition for a value it writes through a parameter.
Preserving a parameter and proving that it carries context are different claims.
