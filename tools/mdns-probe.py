import socket, struct, time, sys
def qname(n): return b"".join(bytes([len(p)])+p.encode() for p in n.split(".") if p)+b"\0"
def name(b,o,depth=0):
    out=[]
    while True:
        l=b[o]
        if l==0: return ".".join(out), o+1
        if l&0xC0==0xC0:
            p=((l&0x3F)<<8)|b[o+1]; n,_=name(b,p,depth+1); out.append(n); return ".".join(out), o+2
        out.append(b[o+1:o+1+l].decode(errors="replace")); o+=1+l
def parse(b):
    qd,an,ns,ar=struct.unpack(">4H",b[4:12]); o=12
    for _ in range(qd): _,o=name(b,o); o+=4
    recs=[]
    for _ in range(an+ns+ar):
        n,o=name(b,o); t,c,ttl,l=struct.unpack(">HHIH",b[o:o+10]); o+=10; d=b[o:o+l]
        if t==12: v=name(b,o)[0]
        elif t==33: v="port=%d target=%s"%(struct.unpack(">H",d[4:6])[0], name(b,o+6)[0])
        elif t==16:
            v=[];i=0
            while i<len(d): v.append(d[i+1:i+1+d[i]].decode(errors="replace")); i+=1+d[i]
        elif t==1: v=socket.inet_ntoa(d)
        else: v="type%d len%d"%(t,l)
        recs.append((n,{12:"PTR",33:"SRV",16:"TXT",1:"A",28:"AAAA",47:"NSEC"}.get(t,t),v)); o+=l
    return recs
svc=sys.argv[1]
# Optional target IP: unicast query to <ip>:5353 instead of the multicast group.
dest=(sys.argv[2] if len(sys.argv)>2 else "224.0.0.251", 5353)
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.setsockopt(socket.IPPROTO_IP,socket.IP_MULTICAST_TTL,255)
s.bind(("0.0.0.0",0)); s.settimeout(0.5)
# QU bit set (0x8001) asks for unicast reply to our ephemeral port
q=struct.pack(">6H",0,0,1,0,0,0)+qname(svc)+struct.pack(">HH",12,0x8001)
end=time.time()+5; last=0
while time.time()<end:
    if time.time()-last>1.5: s.sendto(q,dest); last=time.time()
    try: b,a=s.recvfrom(9000)
    except socket.timeout: continue
    try: recs=parse(b)
    except Exception as e: print(a[0],"unparsed",e,len(b)); continue
    print("from",a[0]); [print("  ",*r) for r in recs]
print("done, sent to", dest[0])
