// "먼저 써 본 사람들" release gate (ADR-0073, PR D).
//
// The quotes themselves live in main.tsx as `ml()` pairs. Each carries a
// `stub` flag: placeholder copy written before real users agreed to be quoted.
// Stub quotes are visible only when the development UI flag is explicitly
// true. Missing or misspelled build configuration therefore fails closed and
// keeps invented testimonials out of every publishable bundle.
export interface EarlyVoice {
  id: string;
  quote: string;
  attribution: string;
  stub: boolean;
}

export const allowStubVoices = import.meta.env.VITE_ALLOW_DEV_AUTH_UI === "true";
export const isReleaseBuild = !allowStubVoices;

export function publishableVoices(
  voices: readonly EarlyVoice[],
  allowStubs: boolean = allowStubVoices,
): readonly EarlyVoice[] {
  return allowStubs ? voices : voices.filter((voice) => !voice.stub);
}
