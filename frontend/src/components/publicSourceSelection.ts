/** Reapply the local checkbox changes after an independent source operation. */
export function rebaseSourceSelection(previous:string[],draft:string[],saved:string[],available:string[]):string[]{
 const wanted=new Set(draft),original=new Set(previous),allowed=new Set(available);
 const removed=new Set(previous.filter(id=>!wanted.has(id)));
 return [...new Set([...saved.filter(id=>!removed.has(id)),...draft.filter(id=>!original.has(id))])].filter(id=>allowed.has(id));
}
