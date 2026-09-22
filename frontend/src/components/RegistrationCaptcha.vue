<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref} from 'vue';
import {Check,ChevronRight,LoaderCircle,RefreshCw,ShieldCheck} from 'lucide-vue-next';
import {useApi,isCancelled} from '../lib/api';
import {t} from '../i18n';
type Challenge={token:string;width:number;height:number;piece_width:number;piece_y:number;background:string;piece:string;expires:number};
type Point={x:number;t:number};
const emit=defineEmits<{'update:modelValue':[value:string]}>();
defineProps<{modelValue:string}>();
const api=useApi(),challenge=ref<Challenge|null>(null),position=ref(0),busy=ref(false),verified=ref(false),error=ref(''),track=ref<HTMLElement|null>(null),dragging=ref(false);
const max=computed(()=>challenge.value?challenge.value.width-challenge.value.piece_width:1),progress=computed(()=>position.value/max.value*100);
let points:Point[]=[],started=0,startX=0,startPosition=0,expiry:ReturnType<typeof setTimeout>|undefined,sequence=0,activePointer:number|null=null;
const message=computed(()=>busy.value?t('正在验证…'):verified.value?t('验证通过'):error.value||t('拖动滑块，使拼图与缺口对齐'));
function expire(at:number){clearTimeout(expiry);expiry=setTimeout(()=>{verified.value=false;challenge.value=null;emit('update:modelValue','');error.value=t('验证已过期，请刷新重试');},Math.max(0,at*1000-Date.now()));}
async function load(){
 if(busy.value)return;const current=++sequence;busy.value=true;error.value='';verified.value=false;challenge.value=null;position.value=0;points=[];dragging.value=false;activePointer=null;clearTimeout(expiry);emit('update:modelValue','');
 try{const value=await api<Challenge>('/register/challenge');if(current!==sequence)return;challenge.value=value;expire(value.expires);}
 catch(e){if(!isCancelled(e))error.value=t((e as Error).message);}finally{if(current===sequence)busy.value=false;}
}
function begin(){if(!points.length){started=performance.now();points=[{x:0,t:0}];}}
function sample(){const p={x:position.value,t:Math.round(performance.now()-started)};if(points.length<120)points.push(p);else points[points.length-1]=p;}
function down(e:PointerEvent){if(!challenge.value||busy.value||verified.value||e.button!==0||activePointer!==null)return;e.preventDefault();begin();activePointer=e.pointerId;startX=e.clientX;startPosition=position.value;dragging.value=true;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);}
function move(e:PointerEvent){if(!dragging.value||e.pointerId!==activePointer)return;const width=Math.max(1,(track.value?.getBoundingClientRect().width||320)-44);position.value=Math.min(max.value,Math.max(0,startPosition+(e.clientX-startX)/width*max.value));sample();}
async function up(e:PointerEvent){if(!dragging.value||e.pointerId!==activePointer)return;move(e);dragging.value=false;activePointer=null;(e.currentTarget as HTMLElement).releasePointerCapture(e.pointerId);await verify();}
function cancel(){dragging.value=false;activePointer=null;position.value=0;points=[];}
function key(e:KeyboardEvent){if(!challenge.value||busy.value||verified.value)return;if(['ArrowLeft','ArrowRight','Home','End','Enter',' '].includes(e.key)){e.preventDefault();begin();if(e.key==='Enter'||e.key===' '){void verify();return;}position.value=e.key==='Home'?0:e.key==='End'?max.value:Math.min(max.value,Math.max(0,position.value+(e.key==='ArrowRight'?1:-1)*(e.shiftKey?10:2)));sample();}}
async function verify(){
 if(!challenge.value||busy.value||verified.value)return;sample();busy.value=true;error.value='';
 try{const value=await api<{token:string;expires:number}>('/register/verify','POST',{token:challenge.value.token,position:Math.round(position.value),trace:points});verified.value=true;emit('update:modelValue',value.token);expire(value.expires);}
 catch(e){if(!isCancelled(e)){error.value=t((e as Error).message);challenge.value=null;emit('update:modelValue','');}}
 finally{busy.value=false;points=[];}
}
onMounted(load);onBeforeUnmount(()=>{sequence++;clearTimeout(expiry);});
</script>
<template>
<section class="captcha-box" :class="{verified,failed:!!error}" :aria-label="t('拼图滑动验证')" :aria-busy="busy">
 <header><span><ShieldCheck :size="15"/>{{t('安全验证')}}</span><button type="button" class="captcha-refresh" :disabled="busy" @click="load" :aria-label="t('刷新验证图片')"><RefreshCw :size="15"/>{{t('刷新')}}</button></header>
 <template v-if="challenge&&!verified">
  <div class="captcha-scene" :style="{aspectRatio:challenge.width+'/'+challenge.height}"><img :src="challenge.background" :alt="t('拖动拼图补全庭院图片')" draggable="false"/><img class="captcha-piece" :src="challenge.piece" alt="" draggable="false" :style="{width:challenge.piece_width/challenge.width*100+'%',left:position/challenge.width*100+'%',top:challenge.piece_y/challenge.height*100+'%'}"/><span class="captcha-scene-caption">GUANGYUE · {{t('月映珠江')}}</span></div>
  <div ref="track" class="captcha-track" :class="{dragging}"><div class="captcha-fill" :style="{width:progress+'%'}"/><span>{{t('向右拖动完成拼图')}}</span><button type="button" class="captcha-thumb" role="slider" :aria-label="t('拼图滑块，方向键调整，回车验证')" :aria-valuemin="0" :aria-valuemax="max" :aria-valuenow="Math.round(position)" :disabled="busy" :style="{left:`calc(${progress}% - ${progress*0.44}px)`}" @pointerdown="down" @pointermove="move" @pointerup="up" @pointercancel="cancel" @keydown="key"><LoaderCircle v-if="busy" class="spin" :size="19"/><ChevronRight v-else :size="21"/></button></div>
 </template>
 <div v-else-if="!verified" class="captcha-placeholder"><LoaderCircle v-if="busy" class="spin" :size="22"/><button v-else type="button" @click="load">{{t('重新验证')}}</button></div>
 <p class="captcha-status" role="status" aria-live="polite"><Check v-if="verified" :size="16"/>{{message}}</p>
</section>
</template>
<style scoped>
.captcha-box{border:1px solid var(--border);border-radius:12px;padding:12px;margin:16px 0;background:var(--surface-raised);min-width:0}.captcha-box header{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:10px}.captcha-box header>span{display:flex;align-items:center;gap:7px;font-size:12px;color:var(--secondary)}.captcha-box .captcha-refresh{height:28px;min-height:28px;padding:3px 6px;border:0;background:transparent;font-size:11px;display:flex;gap:5px;align-items:center}.captcha-scene{position:relative;width:100%;overflow:hidden;border-radius:7px;background:#284544;user-select:none}.captcha-scene>img:first-child{display:block;width:100%;height:100%}.captcha-piece{position:absolute;filter:drop-shadow(0 2px 3px #0008);pointer-events:none}.captcha-scene-caption{position:absolute;bottom:7px;left:9px;color:#f5ead1;font-size:9px;letter-spacing:.08em;pointer-events:none;text-shadow:0 1px 3px #000}.captcha-track{position:relative;height:44px;border:1px solid var(--border);border-radius:8px;margin-top:12px;background:var(--input);overflow:hidden;user-select:none}.captcha-track>span{display:flex;height:100%;align-items:center;justify-content:center;padding-left:25px;font-size:11px;color:var(--muted)}.captcha-fill{position:absolute;inset:0 auto 0 0;background:var(--accent-soft);pointer-events:none}.captcha-box .captcha-thumb{position:absolute;top:0;width:44px;height:42px;min-height:42px;padding:0;border:0;border-right:1px solid var(--border);border-radius:7px;background:var(--surface);color:var(--accent-text);display:grid;place-items:center;touch-action:none;cursor:grab;box-shadow:1px 0 5px #0002}.dragging .captcha-thumb{cursor:grabbing;background:var(--accent);color:var(--button-text,#163630)}.captcha-thumb:focus-visible{outline:2px solid var(--accent);outline-offset:-3px}.captcha-status{display:flex;align-items:center;gap:7px;min-height:18px;margin:9px 0 0;font-size:11px;line-height:1.6;color:var(--muted)}.captcha-box.verified{border-color:var(--accent)}.verified .captcha-status{color:var(--accent-text);font-size:13px}.failed .captcha-status{color:var(--danger,#d57777)}.captcha-placeholder{min-height:65px;display:grid;place-items:center}
</style>
