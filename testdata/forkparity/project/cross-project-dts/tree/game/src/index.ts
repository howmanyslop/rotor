import { handauth } from "../../lib/src/handauth";
import { regular } from "../../lib/src/regular";
export const run = () => regular() + handauth();
