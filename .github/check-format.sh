#!/bin/sh
# Check that a built program is the format the target needs.
#
# The headers are read directly rather than asking `file`: the wording of its
# answer differs between versions (aarch64 versus ARM64, x86-64 versus x86_64),
# and a release check must not depend on which host ran it.
#
#   usage: check-format.sh <program> <target>/<arch>
set -eu

program="$1"
want="$2"

if [ ! -f "$program" ]; then
	echo "no program at $program" >&2
	exit 1
fi

byte() { od -An -tu1 -j"$1" -N"$2" "$program" | tr -d ' \n'; }
hex() { od -An -tx1 -j"$1" -N"$2" "$program" | tr -d ' \n'; }
le16() { od -An -tu2 -j"$1" -N2 "$program" | tr -d ' \n'; }
le32() { od -An -tu4 -j"$1" -N4 "$program" | tr -d ' \n'; }

magic=$(hex 0 2)
case "$magic" in
7f45) kernel=elf ;;
4d5a) kernel=pe ;;
*)
	echo "$program: not an ELF or PE program (magic $magic)" >&2
	exit 1
	;;
esac

# ELF: e_machine is a 16-bit little-endian field at offset 0x12.
# PE:  the machine is a 16-bit little-endian field just after the "PE\0\0"
#      signature, whose offset is stored at 0x3C.
case "$kernel" in
elf) machine=$(le16 18) ;;
pe)
	pe_offset=$(le32 60)
	machine=$(le16 $((pe_offset + 4)))
	;;
esac

case "$kernel/$machine" in
elf/62) got=linux/amd64 ;;
elf/183) got=linux/arm64 ;;
pe/34404) got=windows/amd64 ;;
pe/43620) got=windows/arm64 ;;
*)
	echo "$program: $kernel machine $machine is not a target this project builds" >&2
	exit 1
	;;
esac

if [ "$got" != "$want" ]; then
	echo "$program is $got, but $want was built" >&2
	exit 1
fi
echo "ok: $program is $want"
